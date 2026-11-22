package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
)

const (
	IdempotencyBasligi = "Idempotency-Key"
	// IdempotencySuresi, bir anahtarın ne kadar süre hatırlanacağıdır.
	IdempotencySuresi = 24 * time.Hour
)

// kaydedici, yanıtı hem istemciye yazar hem de saklamak için kopyalar.
type kaydedici struct {
	http.ResponseWriter
	durum int
	govde bytes.Buffer
}

func (k *kaydedici) WriteHeader(kod int) {
	if k.durum == 0 {
		k.durum = kod
	}
	k.ResponseWriter.WriteHeader(kod)
}

func (k *kaydedici) Write(b []byte) (int, error) {
	if k.durum == 0 {
		k.durum = http.StatusOK
	}
	k.govde.Write(b)
	return k.ResponseWriter.Write(b)
}

// idempotent, Idempotency-Key başlığı olan istekleri bir kez işler. Aynı anahtarla
// tekrar gelen istek (örn. ağ kopması sonrası yeniden deneme) yeni bir ödeme
// oluşturmaz; ilk yanıtın aynısı "Idempotent-Replayed: true" başlığıyla döner.
// Anahtarlar kullanıcı başınadır; başka kullanıcının anahtarı çakışmaz.
func (s *Sunucu) idempotent(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		anahtar := r.Header.Get(IdempotencyBasligi)
		if anahtar == "" {
			next(w, r)
			return
		}
		if len(anahtar) > 255 {
			hataYaz(w, http.StatusBadRequest, "gecersiz_idempotency_anahtari", "Idempotency-Key en fazla 255 karakter olabilir")
			return
		}

		govde, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maksIstekBoyutu))
		if err != nil {
			hataYaz(w, http.StatusRequestEntityTooLarge, "istek_cok_buyuk", "İstek gövdesi çok büyük")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(govde))
		r.ContentLength = int64(len(govde))

		ozet := sha256.New()
		io.WriteString(ozet, r.Method+" "+r.URL.Path+"\n")
		ozet.Write(govde)
		istekOzeti := hex.EncodeToString(ozet.Sum(nil))

		kim := kimlikAl(r)
		kayit := veri.IdempotencyKaydi{KullaniciID: kim.ID, Anahtar: anahtar, IstekOzeti: istekOzeti}
		res := s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&kayit)
		if res.Error != nil {
			isHatasiYaz(w, r, res.Error)
			return
		}

		if res.RowsAffected == 0 {
			var onceki veri.IdempotencyKaydi
			if err := s.DB.Where("kullanici_id = ? AND anahtar = ?", kim.ID, anahtar).First(&onceki).Error; err != nil {
				isHatasiYaz(w, r, err)
				return
			}
			switch {
			case onceki.IstekOzeti != istekOzeti:
				hataYaz(w, http.StatusUnprocessableEntity, "idempotency_uyusmazligi",
					"Bu Idempotency-Key farklı bir istekle kullanılmış")
			case onceki.YanitKod == 0:
				hataYaz(w, http.StatusConflict, "idempotency_isleniyor",
					"Aynı Idempotency-Key ile bir istek hâlâ işleniyor")
			default:
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Idempotent-Replayed", "true")
				w.WriteHeader(onceki.YanitKod)
				w.Write(onceki.YanitGovde)
			}
			return
		}

		kw := &kaydedici{ResponseWriter: w}
		basarili := false
		defer func() {
			// Sunucu hatasında (veya panic'te) kayıt silinir ki istemci tekrar deneyebilsin.
			if !basarili || kw.durum >= 500 || kw.durum == 0 {
				if err := s.DB.Delete(&veri.IdempotencyKaydi{}, kayit.ID).Error; err != nil {
					slog.Error("idempotency kaydı silinemedi", "hata", err)
				}
				return
			}
			err := s.DB.Model(&veri.IdempotencyKaydi{}).Where("id = ?", kayit.ID).
				Updates(map[string]any{"yanit_kod": kw.durum, "yanit_govde": kw.govde.Bytes()}).Error
			if err != nil {
				slog.Error("idempotency yanıtı kaydedilemedi", "hata", err)
			}
		}()
		next(kw, r)
		basarili = true
	}
}

// IdempotencyTemizle, süresi geçmiş anahtarları düzenli aralıklarla siler.
func IdempotencyTemizle(ctx context.Context, db *gorm.DB) {
	tik := time.NewTicker(time.Hour)
	defer tik.Stop()
	for {
		sinir := time.Now().UTC().Add(-IdempotencySuresi)
		if err := db.Where("created_at < ?", sinir).Delete(&veri.IdempotencyKaydi{}).Error; err != nil && ctx.Err() == nil {
			slog.Error("idempotency kayıtları temizlenemedi", "hata", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tik.C:
		}
	}
}

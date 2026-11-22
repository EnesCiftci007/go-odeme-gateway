// Package webhook, kuyruktaki (outbox) olayları imzalayıp dış sisteme teslim eder.
//
// Her istek şu başlıkları taşır:
//
//	X-Odeme-Olay-ID: olay_...   (alıcı tarafta tekrarları ayıklamak için)
//	X-Odeme-Imza:    t=<unix zaman>,v1=<hex HMAC-SHA256(gizli, "<t>.<gövde>")>
//
// Alıcı, imzayı Dogrula ile kontrol etmelidir. Zaman damgası tekrar (replay) saldırılarını önler.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
)

const (
	ImzaBasligi   = "X-Odeme-Imza"
	OlayIDBasligi = "X-Odeme-Olay-ID"

	MaksDeneme   = 8
	tabanBekleme = 5 * time.Second
	maksBekleme  = time.Hour
	topluBoyut   = 20
)

// Imzala, gövde için imza başlığının değerini üretir.
func Imzala(gizli []byte, zaman time.Time, govde []byte) string {
	t := strconv.FormatInt(zaman.Unix(), 10)
	return "t=" + t + ",v1=" + hex.EncodeToString(mac(gizli, t, govde))
}

func mac(gizli []byte, t string, govde []byte) []byte {
	h := hmac.New(sha256.New, gizli)
	h.Write([]byte(t))
	h.Write([]byte("."))
	h.Write(govde)
	return h.Sum(nil)
}

var ErrGecersizImza = errors.New("geçersiz webhook imzası")

// Dogrula, alıcı tarafta imzayı ve zaman damgasını kontrol eder.
func Dogrula(gizli []byte, baslik string, govde []byte, tolerans time.Duration, simdi time.Time) error {
	var t, v1 string
	for _, parca := range strings.Split(baslik, ",") {
		k, v, _ := strings.Cut(parca, "=")
		switch k {
		case "t":
			t = v
		case "v1":
			v1 = v
		}
	}
	sn, err := strconv.ParseInt(t, 10, 64)
	if err != nil || v1 == "" {
		return ErrGecersizImza
	}
	if fark := simdi.Sub(time.Unix(sn, 0)); fark > tolerans || fark < -tolerans {
		return ErrGecersizImza
	}
	beklenen := mac(gizli, t, govde)
	gelen, err := hex.DecodeString(v1)
	if err != nil || !hmac.Equal(beklenen, gelen) {
		return ErrGecersizImza
	}
	return nil
}

// Bekleme, n. başarısız denemeden sonra ne kadar bekleneceğini döndürür (üstel artış).
func Bekleme(deneme int) time.Duration {
	d := tabanBekleme << min(deneme, 20)
	return min(d, maksBekleme)
}

type Gonderici struct {
	DB     *gorm.DB
	URL    string
	Gizli  []byte
	Client *http.Client
	// Aralik, kuyruğun ne sıklıkla kontrol edileceğidir.
	Aralik time.Duration
	// Sonuc, her teslim denemesinden sonra çağrılır (metrikler için). nil olabilir.
	Sonuc func(basarili bool)
	Simdi func() time.Time
}

func (g *Gonderici) simdi() time.Time {
	if g.Simdi != nil {
		return g.Simdi()
	}
	return time.Now().UTC()
}

// Calistir, ctx iptal edilene kadar kuyruğu işler.
// Not: tek bir sunucu örneği varsayılır. Birden fazla örnek çalışacaksa PostgreSQL'de
// "SELECT ... FOR UPDATE SKIP LOCKED" ile satır kilitlemesi eklenmelidir.
func (g *Gonderici) Calistir(ctx context.Context) {
	aralik := g.Aralik
	if aralik == 0 {
		aralik = 2 * time.Second
	}
	tik := time.NewTicker(aralik)
	defer tik.Stop()
	for {
		if _, err := g.Isle(ctx); err != nil && ctx.Err() == nil {
			slog.Error("webhook kuyruğu işlenemedi", "hata", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tik.C:
		}
	}
}

// Isle, zamanı gelmiş olayları bir kez gönderir ve kaç olay denendiğini döndürür.
func (g *Gonderici) Isle(ctx context.Context) (int, error) {
	var olaylar []veri.WebhookOlayi
	err := g.DB.WithContext(ctx).
		Where("durum = ? AND sonraki_deneme <= ?", veri.OlayBekliyor, g.simdi()).
		Order("id").Limit(topluBoyut).Find(&olaylar).Error
	if err != nil {
		return 0, err
	}
	for _, o := range olaylar {
		if ctx.Err() != nil {
			break
		}
		gonderimHatasi := g.gonder(ctx, o)
		if g.Sonuc != nil {
			g.Sonuc(gonderimHatasi == nil)
		}

		guncelle := map[string]any{"deneme": o.Deneme + 1}
		switch {
		case gonderimHatasi == nil:
			guncelle["durum"] = veri.OlayGonderildi
			guncelle["son_hata"] = ""
		case o.Deneme+1 >= MaksDeneme:
			guncelle["durum"] = veri.OlayBasarisiz
			guncelle["son_hata"] = kirp(gonderimHatasi.Error())
			slog.Warn("webhook teslim edilemedi, vazgeçildi", "olay", o.OlayID, "hata", gonderimHatasi)
		default:
			guncelle["sonraki_deneme"] = g.simdi().Add(Bekleme(o.Deneme))
			guncelle["son_hata"] = kirp(gonderimHatasi.Error())
		}
		if err := g.DB.Model(&veri.WebhookOlayi{}).Where("id = ?", o.ID).Updates(guncelle).Error; err != nil {
			return len(olaylar), err
		}
	}
	return len(olaylar), nil
}

func (g *Gonderici) gonder(ctx context.Context, o veri.WebhookOlayi) error {
	ctx, iptal := context.WithTimeout(ctx, 10*time.Second)
	defer iptal()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.URL, bytes.NewReader(o.Govde))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "go-odeme-gateway-webhook/1")
	req.Header.Set(OlayIDBasligi, o.OlayID)
	req.Header.Set(ImzaBasligi, Imzala(g.Gizli, g.simdi(), o.Govde))

	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("alıcı %d döndürdü", resp.StatusCode)
	}
	return nil
}

func kirp(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

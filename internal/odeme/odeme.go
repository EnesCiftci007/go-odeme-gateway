// Package odeme, kart, ödeme, iade ve çift taraflı defter işlemlerini içerir.
package odeme

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/para"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
)

// MaxTutar, tek bir işlemde veya bakiyede izin verilen en yüksek tutardır (10 milyon TL).
const MaxTutar para.Kurus = 10_000_000 * 100

const (
	maksTopluKartSayisi = 1000
	maksAciklamaBoyu    = 140
	VarsayilanSayfa     = 20
	MaksSayfa           = 100
)

// Webhook olay türleri.
const (
	OlayOdemeBasarili   = "odeme.basarili"
	OlayOdemeReddedildi = "odeme.reddedildi"
	OlayOdemeIade       = "odeme.iade"
)

// Hata, istemciye gösterilebilecek bir iş kuralı hatasıdır. Kod makine tarafından okunur.
type Hata struct {
	Kod   string
	Mesaj string
}

func (h *Hata) Error() string { return h.Mesaj }

// Is, aynı koda sahip hataları eşit sayar; böylece mesajı özelleştirilmiş bir hata da
// errors.Is(err, ErrGecersizKartNo) ile tanınır.
func (h *Hata) Is(hedef error) bool {
	t, ok := hedef.(*Hata)
	return ok && t.Kod == h.Kod
}

var (
	ErrKartBulunamadi   = &Hata{"kart_bulunamadi", "kart bulunamadı"}
	ErrOdemeBulunamadi  = &Hata{"odeme_bulunamadi", "ödeme bulunamadı"}
	ErrYetersizBakiye   = &Hata{"yetersiz_bakiye", "yetersiz bakiye: işlem gerçekleştirilemedi"}
	ErrGecersizKartNo   = &Hata{"gecersiz_kart_no", "geçersiz kart numarası"}
	ErrGecersizTutar    = &Hata{"gecersiz_tutar", fmt.Sprintf("tutar 0'dan büyük ve en fazla %s TL olmalı", MaxTutar)}
	ErrKartBelirtilmedi = &Hata{"kart_belirtilmedi", "kart_id veya kart_no alanlarından biri gerekli"}
	ErrAciklamaUzun     = &Hata{"aciklama_uzun", fmt.Sprintf("açıklama en fazla %d karakter olabilir", maksAciklamaBoyu)}
	ErrCokFazlaKart     = &Hata{"cok_fazla_kart", fmt.Sprintf("tek seferde en fazla %d kart yüklenebilir", maksTopluKartSayisi)}
	ErrIadeEdilemez     = &Hata{"iade_edilemez", "bu ödeme iade edilemez"}
	ErrIadeTutariFazla  = &Hata{"iade_tutari_fazla", "iade tutarı, iade edilebilir tutardan fazla"}
	ErrSahipBulunamadi  = &Hata{"sahip_bulunamadi", "kart sahibi olarak belirtilen kullanıcı bulunamadı"}
)

var kartNoDeseni = regexp.MustCompile(`^[0-9]{4}([ -]?[0-9]{2,4}){1,4}$`)

func KartNoGecerli(kartNo string) bool {
	return len(kartNo) <= 25 && kartNoDeseni.MatchString(kartNo)
}

// Maskele, kart numarasının yalnızca son 4 hanesini gösterir: "1111-2222" -> "•••• 2222".
func Maskele(kartNo string) string {
	var rakamlar []rune
	for _, r := range kartNo {
		if r >= '0' && r <= '9' {
			rakamlar = append(rakamlar, r)
		}
	}
	if len(rakamlar) < 4 {
		return "••••"
	}
	return "•••• " + string(rakamlar[len(rakamlar)-4:])
}

func tutarGecerli(t para.Kurus) bool { return t > 0 && t <= MaxTutar }

func yeniRef(onek string) string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand Go 1.24+ ile hata döndürmez
	}
	return onek + hex.EncodeToString(b)
}

type Servis struct {
	db *gorm.DB
	// webhookAcik false ise olay kuyruğuna yazılmaz (WEBHOOK_URL ayarlı değil).
	webhookAcik bool
	// OdemeSonucu, her ödeme sonucunda çağrılır (metrikler için). nil olabilir.
	OdemeSonucu func(durum string)
}

func Yeni(db *gorm.DB, webhookAcik bool) *Servis {
	return &Servis{db: db, webhookAcik: webhookAcik}
}

// ---- Kartlar ----

type KartGorunumu struct {
	ID          uint       `json:"id"`
	KartNo      string     `json:"kart_no"`
	Bakiye      para.Kurus `json:"bakiye"`
	SahipEposta string     `json:"sahip_eposta,omitempty"`
	Olusturma   time.Time  `json:"olusturma"`
}

func kartGorunumu(k veri.Kart) KartGorunumu {
	g := KartGorunumu{ID: k.ID, KartNo: Maskele(k.KartNo), Bakiye: k.Bakiye, Olusturma: k.CreatedAt}
	if k.Sahip != nil {
		g.SahipEposta = k.Sahip.Eposta
	}
	return g
}

// Kartlar, kullanıcının kartlarını döndürür; admin tüm kartları görür.
func (s *Servis) Kartlar(kim kimlik.Kimlik) ([]KartGorunumu, error) {
	q := s.db.Preload("Sahip").Order("id")
	if !kim.Admin() {
		q = q.Where("sahip_id = ?", kim.ID)
	}
	var kartlar []veri.Kart
	if err := q.Find(&kartlar).Error; err != nil {
		return nil, err
	}
	sonuc := make([]KartGorunumu, len(kartlar))
	for i, k := range kartlar {
		sonuc[i] = kartGorunumu(k)
	}
	return sonuc, nil
}

type YeniKart struct {
	KartNo      string     `json:"kart_no"`
	Bakiye      para.Kurus `json:"bakiye"`
	SahipEposta string     `json:"sahip_eposta,omitempty"`
}

// TopluKartEkle, kartları tek transaction içinde ekler; zaten var olan kartları atlar.
// Kartlardan biri geçersizse hiçbiri eklenmez. Başlangıç bakiyesi deftere
// "sistem" hesabından karta yükleme olarak yazılır.
func (s *Servis) TopluKartEkle(kartlar []YeniKart) (int, error) {
	if len(kartlar) > maksTopluKartSayisi {
		return 0, ErrCokFazlaKart
	}
	for i, k := range kartlar {
		if !KartNoGecerli(k.KartNo) {
			return 0, &Hata{ErrGecersizKartNo.Kod, fmt.Sprintf("%d. kart: %s", i+1, ErrGecersizKartNo.Mesaj)}
		}
		if k.Bakiye < 0 || k.Bakiye > MaxTutar {
			return 0, &Hata{ErrGecersizTutar.Kod, fmt.Sprintf("%d. kart: bakiye 0 ile %s TL arasında olmalı", i+1, MaxTutar)}
		}
	}

	eklenen := 0
	err := s.db.Transaction(func(tx *gorm.DB) error {
		sahipler := map[string]uint{}
		for _, k := range kartlar {
			e := kimlik.EpostaNormalize(k.SahipEposta)
			if e == "" {
				continue
			}
			if _, ok := sahipler[e]; ok {
				continue
			}
			var kul veri.Kullanici
			if err := tx.Where("eposta = ?", e).First(&kul).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return &Hata{ErrSahipBulunamadi.Kod, ErrSahipBulunamadi.Mesaj + ": " + e}
				}
				return err
			}
			sahipler[e] = kul.ID
		}

		for _, k := range kartlar {
			kart := veri.Kart{KartNo: k.KartNo, Bakiye: k.Bakiye}
			if id, ok := sahipler[kimlik.EpostaNormalize(k.SahipEposta)]; ok {
				kart.SahipID = &id
			}
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&kart)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}
			eklenen++
			if kart.Bakiye > 0 {
				if err := defterYaz(tx, nil, veri.TurYukleme, veri.HesapSistem, veri.KartHesabi(kart.ID), kart.Bakiye); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return eklenen, nil
}

// defterYaz, "kaynak" hesaptan "hedef" hesaba tutar kadar para aktarımını iki satır olarak yazar.
func defterYaz(tx *gorm.DB, odemeID *uint, tur, kaynak, hedef string, tutar para.Kurus) error {
	return tx.Create([]veri.Hareket{
		{OdemeID: odemeID, Hesap: kaynak, Tutar: -tutar, Tur: tur},
		{OdemeID: odemeID, Hesap: hedef, Tutar: tutar, Tur: tur},
	}).Error
}

// ---- Ödemeler ----

type OdemeIstegi struct {
	KartID   uint       `json:"kart_id,omitempty"`
	KartNo   string     `json:"kart_no,omitempty"`
	Tutar    para.Kurus `json:"tutar"`
	Aciklama string     `json:"aciklama,omitempty"`
}

type OdemeGorunumu struct {
	ID             string     `json:"id"`
	KartID         uint       `json:"kart_id"`
	KartNo         string     `json:"kart_no"`
	Tutar          para.Kurus `json:"tutar"`
	IadeEdilen     para.Kurus `json:"iade_edilen"`
	IadeEdilebilir para.Kurus `json:"iade_edilebilir"`
	Durum          string     `json:"durum"`
	Aciklama       string     `json:"aciklama,omitempty"`
	Olusturma      time.Time  `json:"olusturma"`
}

func odemeGorunumu(o veri.Odeme) OdemeGorunumu {
	g := OdemeGorunumu{
		ID: o.RefNo, KartID: o.KartID, KartNo: Maskele(o.Kart.KartNo), Tutar: o.Tutar,
		IadeEdilen: o.IadeEdilen, Durum: o.Durum, Aciklama: o.Aciklama, Olusturma: o.CreatedAt,
	}
	if o.Durum == veri.DurumBasarili || o.Durum == veri.DurumKismiIade {
		g.IadeEdilebilir = o.Tutar - o.IadeEdilen
	}
	return g
}

type OdemeSonucu struct {
	Odeme       OdemeGorunumu `json:"odeme"`
	KalanBakiye para.Kurus    `json:"kalan_bakiye"`
}

// kartBul, kullanıcının erişebildiği kartı bulur. Başkasının kartı "bulunamadı" olarak
// döner; böylece kart numaralarının var olup olmadığı yoklanamaz.
func kartBul(tx *gorm.DB, kim kimlik.Kimlik, kartID uint, kartNo string) (veri.Kart, error) {
	var kart veri.Kart
	q := tx
	switch {
	case kartID != 0:
		q = q.Where("id = ?", kartID)
	case kartNo != "":
		if !KartNoGecerli(kartNo) {
			return kart, ErrGecersizKartNo
		}
		q = q.Where("kart_no = ?", kartNo)
	default:
		return kart, ErrKartBelirtilmedi
	}
	if !kim.Admin() {
		q = q.Where("sahip_id = ?", kim.ID)
	}
	if err := q.First(&kart).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return kart, ErrKartBulunamadi
		}
		return kart, err
	}
	return kart, nil
}

// OdemeYap, karttan tutarı düşer. Bakiye her zaman veritabanından okunur.
// Düşme işlemi tek bir koşullu UPDATE ile yapılır, böylece eşzamanlı ödemeler
// kartı eksi bakiyeye düşüremez. Reddedilen ödemeler de kayda geçer.
func (s *Servis) OdemeYap(kim kimlik.Kimlik, ist OdemeIstegi) (OdemeSonucu, error) {
	if !tutarGecerli(ist.Tutar) {
		return OdemeSonucu{}, ErrGecersizTutar
	}
	ist.Aciklama = strings.TrimSpace(ist.Aciklama)
	if len([]rune(ist.Aciklama)) > maksAciklamaBoyu {
		return OdemeSonucu{}, ErrAciklamaUzun
	}

	var sonuc OdemeSonucu
	var isHatasi error
	err := s.db.Transaction(func(tx *gorm.DB) error {
		kart, err := kartBul(tx, kim, ist.KartID, ist.KartNo)
		if err != nil {
			return err
		}

		res := tx.Model(&veri.Kart{}).
			Where("id = ? AND bakiye_kurus >= ?", kart.ID, ist.Tutar).
			Update("bakiye_kurus", gorm.Expr("bakiye_kurus - ?", ist.Tutar))
		if res.Error != nil {
			return res.Error
		}
		if err := tx.First(&kart, kart.ID).Error; err != nil {
			return err
		}

		o := veri.Odeme{
			RefNo: yeniRef("odm_"), KartID: kart.ID, Kart: kart, KullaniciID: kim.ID,
			Tutar: ist.Tutar, Durum: veri.DurumBasarili, Aciklama: ist.Aciklama,
		}
		olay := OlayOdemeBasarili
		if res.RowsAffected == 0 {
			o.Durum = veri.DurumReddedildi
			olay = OlayOdemeReddedildi
			isHatasi = ErrYetersizBakiye
		}
		if err := tx.Omit("Kart").Create(&o).Error; err != nil {
			return err
		}
		if o.Durum == veri.DurumBasarili {
			if err := defterYaz(tx, &o.ID, veri.TurOdeme, veri.KartHesabi(kart.ID), veri.HesapIsyeri, o.Tutar); err != nil {
				return err
			}
		}

		sonuc = OdemeSonucu{Odeme: odemeGorunumu(o), KalanBakiye: kart.Bakiye}
		return s.olayYaz(tx, olay, sonuc.Odeme)
	})
	if err != nil {
		return OdemeSonucu{}, err
	}
	if s.OdemeSonucu != nil {
		s.OdemeSonucu(sonuc.Odeme.Durum)
	}
	return sonuc, isHatasi
}

func odemeSorgusu(tx *gorm.DB, kim kimlik.Kimlik) *gorm.DB {
	q := tx.Model(&veri.Odeme{}).Preload("Kart")
	if !kim.Admin() {
		q = q.Where("kullanici_id = ?", kim.ID)
	}
	return q
}

// Odeme, tek bir ödemeyi döndürür. Kullanıcı yalnızca kendi ödemelerini görebilir.
func (s *Servis) Odeme(kim kimlik.Kimlik, refNo string) (OdemeGorunumu, error) {
	var o veri.Odeme
	if err := odemeSorgusu(s.db, kim).Where("ref_no = ?", refNo).First(&o).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return OdemeGorunumu{}, ErrOdemeBulunamadi
		}
		return OdemeGorunumu{}, err
	}
	return odemeGorunumu(o), nil
}

type OdemeSayfasi struct {
	Odemeler []OdemeGorunumu `json:"odemeler"`
	// SonrakiImlec, bir sonraki sayfa için "once" parametresine verilir. Boşsa sayfa yoktur.
	SonrakiImlec string `json:"sonraki_imlec,omitempty"`
}

// Odemeler, ödemeleri yeniden eskiye sayfalı döndürür (imleç tabanlı sayfalama).
func (s *Servis) Odemeler(kim kimlik.Kimlik, limit int, onceRef string) (OdemeSayfasi, error) {
	if limit <= 0 {
		limit = VarsayilanSayfa
	}
	limit = min(limit, MaksSayfa)

	q := odemeSorgusu(s.db, kim)
	if onceRef != "" {
		var imlec veri.Odeme
		if err := s.db.Select("id").Where("ref_no = ?", onceRef).First(&imlec).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return OdemeSayfasi{}, &Hata{"gecersiz_imlec", "geçersiz sayfa imleci"}
			}
			return OdemeSayfasi{}, err
		}
		q = q.Where("id < ?", imlec.ID)
	}

	var liste []veri.Odeme
	if err := q.Order("id desc").Limit(limit + 1).Find(&liste).Error; err != nil {
		return OdemeSayfasi{}, err
	}
	sayfa := OdemeSayfasi{Odemeler: make([]OdemeGorunumu, 0, limit)}
	if len(liste) > limit {
		liste = liste[:limit]
		sayfa.SonrakiImlec = liste[limit-1].RefNo
	}
	for _, o := range liste {
		sayfa.Odemeler = append(sayfa.Odemeler, odemeGorunumu(o))
	}
	return sayfa, nil
}

// ---- İadeler ----

// IadeEt, ödemenin tamamını (tutar nil) veya bir kısmını karta geri yükler.
// Birden fazla kısmi iade yapılabilir; toplamı ödeme tutarını aşamaz.
func (s *Servis) IadeEt(kim kimlik.Kimlik, refNo string, tutar *para.Kurus) (OdemeSonucu, error) {
	if tutar != nil && !tutarGecerli(*tutar) {
		return OdemeSonucu{}, ErrGecersizTutar
	}

	var sonuc OdemeSonucu
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var o veri.Odeme
		if err := odemeSorgusu(tx, kim).Where("ref_no = ?", refNo).First(&o).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrOdemeBulunamadi
			}
			return err
		}
		if o.Durum != veri.DurumBasarili && o.Durum != veri.DurumKismiIade {
			return ErrIadeEdilemez
		}
		miktar := o.Tutar - o.IadeEdilen
		if tutar != nil {
			miktar = *tutar
		}

		// Koşullu UPDATE: eşzamanlı iki iade toplamda ödeme tutarını aşamaz.
		res := tx.Model(&veri.Odeme{}).
			Where("id = ? AND durum IN ? AND tutar_kurus - iade_edilen_kurus >= ?",
				o.ID, []string{veri.DurumBasarili, veri.DurumKismiIade}, miktar).
			Updates(map[string]any{
				"iade_edilen_kurus": gorm.Expr("iade_edilen_kurus + ?", miktar),
				"durum": gorm.Expr("CASE WHEN iade_edilen_kurus + ? = tutar_kurus THEN ? ELSE ? END",
					miktar, veri.DurumIadeEdildi, veri.DurumKismiIade),
				"updated_at": time.Now().UTC(),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrIadeTutariFazla
		}

		if err := tx.Model(&veri.Kart{}).Where("id = ?", o.KartID).
			Update("bakiye_kurus", gorm.Expr("bakiye_kurus + ?", miktar)).Error; err != nil {
			return err
		}
		if err := defterYaz(tx, &o.ID, veri.TurIade, veri.HesapIsyeri, veri.KartHesabi(o.KartID), miktar); err != nil {
			return err
		}

		if err := tx.Preload("Kart").First(&o, o.ID).Error; err != nil {
			return err
		}
		sonuc = OdemeSonucu{Odeme: odemeGorunumu(o), KalanBakiye: o.Kart.Bakiye}
		return s.olayYaz(tx, OlayOdemeIade, map[string]any{"odeme": sonuc.Odeme, "iade_tutari": miktar})
	})
	if err != nil {
		return OdemeSonucu{}, err
	}
	if s.OdemeSonucu != nil {
		s.OdemeSonucu("iade")
	}
	return sonuc, nil
}

// ---- Webhook olayları (outbox) ----

type Olay struct {
	ID        string    `json:"id"`
	Tur       string    `json:"tur"`
	Olusturma time.Time `json:"olusturma"`
	Veri      any       `json:"veri"`
}

func (s *Servis) olayYaz(tx *gorm.DB, tur string, v any) error {
	if !s.webhookAcik {
		return nil
	}
	olay := Olay{ID: yeniRef("olay_"), Tur: tur, Olusturma: time.Now().UTC(), Veri: v}
	govde, err := json.Marshal(olay)
	if err != nil {
		return err
	}
	return tx.Create(&veri.WebhookOlayi{
		OlayID: olay.ID, Tur: tur, Govde: govde, Durum: veri.OlayBekliyor, SonrakiDeneme: time.Now().UTC(),
	}).Error
}

// ---- Mutabakat ----

type MutabakatRaporu struct {
	Tutarli          bool       `json:"tutarli"`
	DefterToplami    para.Kurus `json:"defter_toplami"`
	IsyeriBakiyesi   para.Kurus `json:"isyeri_bakiyesi"`
	KartSayisi       int        `json:"kart_sayisi"`
	UyusmayanKartlar []string   `json:"uyusmayan_kartlar"`
}

// Mutabakat, defterin kendi içinde dengeli olduğunu (tüm hareketlerin toplamı 0)
// ve her kartın bakiyesinin defterdeki hareketlerinin toplamına eşit olduğunu kontrol eder.
func (s *Servis) Mutabakat() (MutabakatRaporu, error) {
	r := MutabakatRaporu{UyusmayanKartlar: []string{}}

	var toplam struct{ T int64 }
	if err := s.db.Model(&veri.Hareket{}).Select("COALESCE(SUM(tutar_kurus), 0) AS t").Scan(&toplam).Error; err != nil {
		return r, err
	}
	r.DefterToplami = para.Kurus(toplam.T)

	var hesaplar []struct {
		Hesap string
		T     int64
	}
	if err := s.db.Model(&veri.Hareket{}).Select("hesap, SUM(tutar_kurus) AS t").Group("hesap").Scan(&hesaplar).Error; err != nil {
		return r, err
	}
	defter := map[string]para.Kurus{}
	for _, h := range hesaplar {
		defter[h.Hesap] = para.Kurus(h.T)
	}
	r.IsyeriBakiyesi = defter[veri.HesapIsyeri]

	var kartlar []veri.Kart
	if err := s.db.Order("id").Find(&kartlar).Error; err != nil {
		return r, err
	}
	r.KartSayisi = len(kartlar)
	for _, k := range kartlar {
		if defter[veri.KartHesabi(k.ID)] != k.Bakiye {
			r.UyusmayanKartlar = append(r.UyusmayanKartlar,
				fmt.Sprintf("%s: bakiye %s, defter %s", Maskele(k.KartNo), k.Bakiye, defter[veri.KartHesabi(k.ID)]))
		}
	}
	r.Tutarli = r.DefterToplami == 0 && len(r.UyusmayanKartlar) == 0
	return r, nil
}

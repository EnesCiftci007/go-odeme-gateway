package odeme

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/para"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri/veritest"
)

type ortam struct {
	db    *gorm.DB
	s     *Servis
	admin kimlik.Kimlik
	ali   kimlik.Kimlik
	ayse  kimlik.Kimlik
}

func kur(t *testing.T) ortam {
	t.Helper()
	db := veritest.Ac(t)
	ks, err := kimlik.Yeni(db, "test-anahtari-en-az-otuz-iki-karakter-uzunlugunda", bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	kim := func(e, rol string) kimlik.Kimlik {
		k, err := ks.KullaniciOlustur(e, "sifre-12345", rol)
		if err != nil {
			t.Fatal(err)
		}
		return kimlik.Kimlik{ID: k.ID, Eposta: k.Eposta, Rol: k.Rol}
	}
	o := ortam{db: db, s: Yeni(db, true),
		admin: kim("admin@test.com", veri.RolAdmin),
		ali:   kim("ali@test.com", veri.RolKullanici),
		ayse:  kim("ayse@test.com", veri.RolKullanici),
	}
	_, err = o.s.TopluKartEkle([]YeniKart{
		{KartNo: "1111-2222", Bakiye: 100000, SahipEposta: "ali@test.com"},
		{KartNo: "3333-4444", Bakiye: 50000, SahipEposta: "ayse@test.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (o ortam) mutabakatTutarli(t *testing.T) {
	t.Helper()
	r, err := o.s.Mutabakat()
	if err != nil {
		t.Fatal(err)
	}
	if !r.Tutarli {
		t.Fatalf("defter tutarsız: %+v", r)
	}
}

func (o ortam) bakiye(t *testing.T, kartNo string) para.Kurus {
	t.Helper()
	var k veri.Kart
	if err := o.db.Where("kart_no = ?", kartNo).First(&k).Error; err != nil {
		t.Fatal(err)
	}
	return k.Bakiye
}

func TestMaskele(t *testing.T) {
	for girdi, beklenen := range map[string]string{
		"1111-2222": "•••• 2222", "1234 5678 9012 3456": "•••• 3456", "12": "••••",
	} {
		if got := Maskele(girdi); got != beklenen {
			t.Errorf("Maskele(%q) = %q", girdi, got)
		}
	}
}

func TestOdemeBasarili(t *testing.T) {
	o := kur(t)
	s, err := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 25050, Aciklama: "kitap"})
	if err != nil {
		t.Fatal(err)
	}
	if s.KalanBakiye != 74950 || s.Odeme.Durum != veri.DurumBasarili || s.Odeme.KartNo != "•••• 2222" {
		t.Fatalf("%+v", s)
	}
	if s.Odeme.IadeEdilebilir != 25050 {
		t.Fatalf("iade edilebilir: %v", s.Odeme.IadeEdilebilir)
	}
	o.mutabakatTutarli(t)
}

func TestOdemeKartIDIle(t *testing.T) {
	o := kur(t)
	kartlar, _ := o.s.Kartlar(o.ali)
	if len(kartlar) != 1 {
		t.Fatalf("ali'nin 1 kartı olmalı: %+v", kartlar)
	}
	if _, err := o.s.OdemeYap(o.ali, OdemeIstegi{KartID: kartlar[0].ID, Tutar: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestOdemeYetersizBakiyeKaydedilir(t *testing.T) {
	o := kur(t)
	s, err := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 100001})
	if !errors.Is(err, ErrYetersizBakiye) || s.KalanBakiye != 100000 || s.Odeme.Durum != veri.DurumReddedildi {
		t.Fatalf("%+v %v", s, err)
	}
	sayfa, _ := o.s.Odemeler(o.ali, 10, "")
	if len(sayfa.Odemeler) != 1 || sayfa.Odemeler[0].IadeEdilebilir != 0 {
		t.Fatalf("reddedilen ödeme kayda geçmeli: %+v", sayfa)
	}
	o.mutabakatTutarli(t)
}

func TestBaskasininKartiKullanilamaz(t *testing.T) {
	o := kur(t)
	if _, err := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "3333-4444", Tutar: 100}); !errors.Is(err, ErrKartBulunamadi) {
		t.Fatalf("err=%v", err)
	}
	if o.bakiye(t, "3333-4444") != 50000 {
		t.Fatal("bakiye değişmemeli")
	}
	// Admin her kartı kullanabilir.
	if _, err := o.s.OdemeYap(o.admin, OdemeIstegi{KartNo: "3333-4444", Tutar: 100}); err != nil {
		t.Fatal(err)
	}
}

func TestOdemeDogrulama(t *testing.T) {
	o := kur(t)
	durumlar := []struct {
		ist OdemeIstegi
		err error
	}{
		{OdemeIstegi{KartNo: "1111-2222", Tutar: 0}, ErrGecersizTutar},
		{OdemeIstegi{KartNo: "1111-2222", Tutar: -500}, ErrGecersizTutar},
		{OdemeIstegi{KartNo: "1111-2222", Tutar: MaxTutar + 1}, ErrGecersizTutar},
		{OdemeIstegi{Tutar: 100}, ErrKartBelirtilmedi},
		{OdemeIstegi{KartNo: "<script>", Tutar: 100}, ErrGecersizKartNo},
		{OdemeIstegi{KartNo: "9999-8888", Tutar: 100}, ErrKartBulunamadi},
	}
	for _, d := range durumlar {
		if _, err := o.s.OdemeYap(o.ali, d.ist); !errors.Is(err, d.err) {
			t.Errorf("%+v: err=%v, beklenen %v", d.ist, err, d.err)
		}
	}
	if o.bakiye(t, "1111-2222") != 100000 {
		t.Fatal("bakiye değişmemeli")
	}
}

// 100 eşzamanlı ödeme; bakiye yalnızca 10 tanesine yeter.
func TestOdemeEszamanli(t *testing.T) {
	o := kur(t)
	var wg sync.WaitGroup
	var basarili atomic.Int32
	for range 100 {
		wg.Go(func() {
			_, err := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 10000})
			if err == nil {
				basarili.Add(1)
			} else if !errors.Is(err, ErrYetersizBakiye) {
				t.Errorf("beklenmeyen hata: %v", err)
			}
		})
	}
	wg.Wait()
	if basarili.Load() != 10 || o.bakiye(t, "1111-2222") != 0 {
		t.Fatalf("basarili=%d bakiye=%v", basarili.Load(), o.bakiye(t, "1111-2222"))
	}
	o.mutabakatTutarli(t)
}

func TestKismiVeTamIade(t *testing.T) {
	o := kur(t)
	s, _ := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 30000})
	ref := s.Odeme.ID

	on := para.Kurus(10000)
	s, err := o.s.IadeEt(o.ali, ref, &on)
	if err != nil || s.Odeme.Durum != veri.DurumKismiIade || s.Odeme.IadeEdilebilir != 20000 || s.KalanBakiye != 80000 {
		t.Fatalf("kısmi iade: %+v %v", s, err)
	}

	fazla := para.Kurus(20001)
	if _, err := o.s.IadeEt(o.ali, ref, &fazla); !errors.Is(err, ErrIadeTutariFazla) {
		t.Fatalf("fazla iade: %v", err)
	}

	s, err = o.s.IadeEt(o.ali, ref, nil) // kalanın tamamı
	if err != nil || s.Odeme.Durum != veri.DurumIadeEdildi || s.Odeme.IadeEdilen != 30000 || s.KalanBakiye != 100000 {
		t.Fatalf("tam iade: %+v %v", s, err)
	}
	if _, err := o.s.IadeEt(o.ali, ref, nil); !errors.Is(err, ErrIadeEdilemez) {
		t.Fatalf("ikinci tam iade: %v", err)
	}
	o.mutabakatTutarli(t)
}

func TestIadeYetkisi(t *testing.T) {
	o := kur(t)
	s, _ := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 5000})
	if _, err := o.s.IadeEt(o.ayse, s.Odeme.ID, nil); !errors.Is(err, ErrOdemeBulunamadi) {
		t.Fatalf("başkasının ödemesi: %v", err)
	}
	if _, err := o.s.Odeme(o.ayse, s.Odeme.ID); !errors.Is(err, ErrOdemeBulunamadi) {
		t.Fatalf("başkasının ödemesi görülmemeli: %v", err)
	}
	if _, err := o.s.IadeEt(o.admin, s.Odeme.ID, nil); err != nil {
		t.Fatalf("admin iade edebilmeli: %v", err)
	}
}

func TestReddedilenOdemeIadeEdilemez(t *testing.T) {
	o := kur(t)
	s, _ := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 999999})
	if _, err := o.s.IadeEt(o.ali, s.Odeme.ID, nil); !errors.Is(err, ErrIadeEdilemez) {
		t.Fatalf("err=%v", err)
	}
	o.mutabakatTutarli(t)
}

// Aynı ödemeye eşzamanlı 20 tam iade; yalnızca biri başarılı olmalı.
func TestIadeEszamanli(t *testing.T) {
	o := kur(t)
	s, _ := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 40000})
	var wg sync.WaitGroup
	var basarili atomic.Int32
	for range 20 {
		wg.Go(func() {
			if _, err := o.s.IadeEt(o.ali, s.Odeme.ID, nil); err == nil {
				basarili.Add(1)
			}
		})
	}
	wg.Wait()
	if basarili.Load() != 1 || o.bakiye(t, "1111-2222") != 100000 {
		t.Fatalf("basarili=%d bakiye=%v", basarili.Load(), o.bakiye(t, "1111-2222"))
	}
	o.mutabakatTutarli(t)
}

func TestSayfalama(t *testing.T) {
	o := kur(t)
	for range 5 {
		o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 100})
	}
	o.s.OdemeYap(o.ayse, OdemeIstegi{KartNo: "3333-4444", Tutar: 100})

	s1, err := o.s.Odemeler(o.ali, 2, "")
	if err != nil || len(s1.Odemeler) != 2 || s1.SonrakiImlec == "" {
		t.Fatalf("%+v %v", s1, err)
	}
	s2, _ := o.s.Odemeler(o.ali, 2, s1.SonrakiImlec)
	s3, _ := o.s.Odemeler(o.ali, 2, s2.SonrakiImlec)
	if len(s2.Odemeler) != 2 || len(s3.Odemeler) != 1 || s3.SonrakiImlec != "" {
		t.Fatalf("s2=%d s3=%d imlec=%q", len(s2.Odemeler), len(s3.Odemeler), s3.SonrakiImlec)
	}
	if s1.Odemeler[0].ID == s2.Odemeler[0].ID {
		t.Fatal("sayfalar çakışıyor")
	}
	hepsi, _ := o.s.Odemeler(o.admin, 100, "")
	if len(hepsi.Odemeler) != 6 {
		t.Fatalf("admin tüm ödemeleri görmeli: %d", len(hepsi.Odemeler))
	}
}

func TestTopluKartEkle(t *testing.T) {
	o := kur(t)
	// Var olan kart atlanır, bakiyesi ezilmez.
	n, err := o.s.TopluKartEkle([]YeniKart{{KartNo: "1111-2222", Bakiye: 999999}, {KartNo: "5555-6666", Bakiye: 10}})
	if err != nil || n != 1 || o.bakiye(t, "1111-2222") != 100000 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Geçersiz kart varsa hiçbiri eklenmez.
	_, err = o.s.TopluKartEkle([]YeniKart{{KartNo: "7777-8888"}, {KartNo: `<img src=x onerror=alert(1)>`}})
	if !errors.Is(err, ErrGecersizKartNo) {
		t.Fatalf("err=%v", err)
	}
	if _, err = o.s.TopluKartEkle([]YeniKart{{KartNo: "7777-8888", Bakiye: -1}}); !errors.Is(err, ErrGecersizTutar) {
		t.Fatal("negatif bakiye reddedilmeli")
	}
	if _, err = o.s.TopluKartEkle([]YeniKart{{KartNo: "7777-8888", SahipEposta: "yok@test.com"}}); !errors.Is(err, ErrSahipBulunamadi) {
		t.Fatal("bilinmeyen sahip reddedilmeli")
	}
	var sayi int64
	o.db.Model(&veri.Kart{}).Count(&sayi)
	if sayi != 3 {
		t.Fatalf("kart sayısı %d, beklenen 3", sayi)
	}
	o.mutabakatTutarli(t)
}

func TestWebhookOlaylariYazilir(t *testing.T) {
	o := kur(t)
	s, _ := o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 100})
	o.s.OdemeYap(o.ali, OdemeIstegi{KartNo: "1111-2222", Tutar: 99999999})
	o.s.IadeEt(o.ali, s.Odeme.ID, nil)

	var turler []string
	o.db.Model(&veri.WebhookOlayi{}).Order("id").Pluck("tur", &turler)
	beklenen := []string{OlayOdemeBasarili, OlayOdemeReddedildi, OlayOdemeIade}
	if len(turler) != 3 || turler[0] != beklenen[0] || turler[1] != beklenen[1] || turler[2] != beklenen[2] {
		t.Fatalf("olaylar: %v", turler)
	}
}

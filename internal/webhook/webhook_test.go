package webhook

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri/veritest"
)

var gizli = []byte("webhook-gizli-anahtari-en-az-32-karakter")

func TestImzaDogrula(t *testing.T) {
	simdi := time.Unix(1_800_000_000, 0)
	govde := []byte(`{"id":"olay_1"}`)
	imza := Imzala(gizli, simdi, govde)

	if err := Dogrula(gizli, imza, govde, 5*time.Minute, simdi.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	durumlar := map[string]func() error{
		"değiştirilmiş gövde": func() error { return Dogrula(gizli, imza, []byte(`{"id":"olay_2"}`), 5*time.Minute, simdi) },
		"yanlış anahtar":      func() error { return Dogrula([]byte("baska"), imza, govde, 5*time.Minute, simdi) },
		"süresi geçmiş":       func() error { return Dogrula(gizli, imza, govde, 5*time.Minute, simdi.Add(10*time.Minute)) },
		"bozuk başlık":        func() error { return Dogrula(gizli, "t=abc,v1=zz", govde, 5*time.Minute, simdi) },
		"boş başlık":          func() error { return Dogrula(gizli, "", govde, 5*time.Minute, simdi) },
	}
	for ad, f := range durumlar {
		if err := f(); !errors.Is(err, ErrGecersizImza) {
			t.Errorf("%s: err=%v", ad, err)
		}
	}
}

func TestBekleme(t *testing.T) {
	if Bekleme(0) != 5*time.Second || Bekleme(1) != 10*time.Second || Bekleme(30) != time.Hour {
		t.Fatal(Bekleme(0), Bekleme(1), Bekleme(30))
	}
}

func olayEkle(t *testing.T, g *Gonderici, id string) {
	t.Helper()
	err := g.DB.Create(&veri.WebhookOlayi{
		OlayID: id, Tur: "odeme.basarili", Govde: []byte(`{"id":"` + id + `"}`),
		Durum: veri.OlayBekliyor, SonrakiDeneme: g.simdi(),
	}).Error
	if err != nil {
		t.Fatal(err)
	}
}

func olayGetir(t *testing.T, g *Gonderici, id string) veri.WebhookOlayi {
	t.Helper()
	var o veri.WebhookOlayi
	if err := g.DB.Where("olay_id = ?", id).First(&o).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

func TestTeslimVeImza(t *testing.T) {
	var gelen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		govde, _ := io.ReadAll(r.Body)
		if err := Dogrula(gizli, r.Header.Get(ImzaBasligi), govde, time.Minute, time.Now()); err != nil {
			t.Errorf("imza doğrulanamadı: %v", err)
		}
		if r.Header.Get(OlayIDBasligi) != "olay_a" {
			t.Errorf("olay id başlığı: %q", r.Header.Get(OlayIDBasligi))
		}
		gelen.Add(1)
	}))
	defer srv.Close()

	g := &Gonderici{DB: veritest.Ac(t), URL: srv.URL, Gizli: gizli}
	olayEkle(t, g, "olay_a")
	if n, err := g.Isle(context.Background()); err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if o := olayGetir(t, g, "olay_a"); o.Durum != veri.OlayGonderildi || gelen.Load() != 1 {
		t.Fatalf("durum=%s gelen=%d", o.Durum, gelen.Load())
	}
	// Gönderilmiş olay tekrar gönderilmez.
	if n, _ := g.Isle(context.Background()); n != 0 {
		t.Fatalf("tekrar gönderildi: %d", n)
	}
}

func TestBasarisizTeslimTekrarDenenir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	simdi := time.Now().UTC()
	g := &Gonderici{DB: veritest.Ac(t), URL: srv.URL, Gizli: gizli, Simdi: func() time.Time { return simdi }}
	olayEkle(t, g, "olay_b")

	for deneme := 1; deneme <= MaksDeneme; deneme++ {
		if n, err := g.Isle(context.Background()); err != nil || n != 1 {
			t.Fatalf("deneme %d: n=%d err=%v", deneme, n, err)
		}
		o := olayGetir(t, g, "olay_b")
		if o.Deneme != deneme {
			t.Fatalf("deneme sayısı %d, beklenen %d", o.Deneme, deneme)
		}
		if deneme < MaksDeneme {
			if o.Durum != veri.OlayBekliyor || o.SonHata == "" {
				t.Fatalf("deneme %d: %+v", deneme, o)
			}
			// Bekleme süresi dolmadan tekrar denenmez.
			if n, _ := g.Isle(context.Background()); n != 0 {
				t.Fatal("bekleme süresi dolmadan tekrar denendi")
			}
			simdi = simdi.Add(Bekleme(deneme - 1))
		} else if o.Durum != veri.OlayBasarisiz {
			t.Fatalf("son denemeden sonra durum %s", o.Durum)
		}
	}
}

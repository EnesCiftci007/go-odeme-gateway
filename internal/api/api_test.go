package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"golang.org/x/crypto/bcrypt"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/odeme"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri/veritest"
)

type testOrtami struct {
	t *testing.T
	s *Sunucu
	h http.Handler
}

func kur(t *testing.T) *testOrtami {
	t.Helper()
	db := veritest.Ac(t)
	ks, err := kimlik.Yeni(db, "test-anahtari-en-az-otuz-iki-karakter-uzunlugunda", bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range [][2]string{{"admin@test.com", veri.RolAdmin}, {"ali@test.com", veri.RolKullanici}, {"ayse@test.com", veri.RolKullanici}} {
		if _, err := ks.KullaniciOlustur(k[0], "sifre-12345", k[1]); err != nil {
			t.Fatal(err)
		}
	}
	os := odeme.Yeni(db, false)
	if _, err := os.TopluKartEkle([]odeme.YeniKart{
		{KartNo: "1111-2222", Bakiye: 100000, SahipEposta: "ali@test.com"},
		{KartNo: "3333-4444", Bakiye: 100000, SahipEposta: "ayse@test.com"},
	}); err != nil {
		t.Fatal(err)
	}
	s := &Sunucu{
		DB: db, Kimlik: ks, Odeme: os,
		Statik:  fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>test</title>")}},
		OpenAPI: []byte("openapi: 3.1.0"),
	}
	return &testOrtami{t: t, s: s, h: s.Handler()}
}

type yanit struct {
	*httptest.ResponseRecorder
	veri map[string]any
}

func (o *testOrtami) istek(metot, yol, token, govde string, basliklar ...string) yanit {
	o.t.Helper()
	r := httptest.NewRequest(metot, yol, strings.NewReader(govde))
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(basliklar); i += 2 {
		r.Header.Set(basliklar[i], basliklar[i+1])
	}
	w := httptest.NewRecorder()
	o.h.ServeHTTP(w, r)
	var v map[string]any
	json.Unmarshal(w.Body.Bytes(), &v)
	return yanit{w, v}
}

func (o *testOrtami) giris(eposta string) string {
	o.t.Helper()
	y := o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"`+eposta+`","sifre":"sifre-12345"}`)
	if y.Code != http.StatusOK {
		o.t.Fatalf("giriş başarısız: %d %s", y.Code, y.Body)
	}
	return y.veri["token"].(string)
}

func (o *testOrtami) kod(y yanit, beklenen int) {
	o.t.Helper()
	if y.Code != beklenen {
		o.t.Fatalf("durum %d, beklenen %d: %s", y.Code, beklenen, y.Body)
	}
}

func TestGiris(t *testing.T) {
	o := kur(t)
	y := o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"ALI@test.com ","sifre":"sifre-12345"}`)
	o.kod(y, 200)
	if y.veri["kullanici"].(map[string]any)["rol"] != "kullanici" {
		t.Fatalf("%v", y.veri)
	}
	o.kod(o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"ali@test.com","sifre":"yanlis-sifre"}`), 401)
	o.kod(o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"ali@test.com"}`), 400)
	o.kod(o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"a","sifre":"b","fazla":1}`), 400)
}

func TestGirisSiniri(t *testing.T) {
	o := kur(t)
	for i := range 5 {
		if y := o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"x@test.com","sifre":"yanlis-sifre"}`); y.Code != 401 {
			t.Fatalf("%d. deneme: %d", i+1, y.Code)
		}
	}
	y := o.istek("POST", "/api/v1/auth/login", "", `{"eposta":"ali@test.com","sifre":"sifre-12345"}`)
	o.kod(y, 429)
	if y.Header().Get("Retry-After") == "" || y.veri["kod"] != "cok_fazla_istek" {
		t.Fatalf("Retry-After/kod eksik: %v", y.veri)
	}
}

func TestOturumGerekli(t *testing.T) {
	o := kur(t)
	for _, yol := range []string{"/api/v1/me", "/api/v1/cards", "/api/v1/payments"} {
		o.kod(o.istek("GET", yol, "", ""), 401)
		o.kod(o.istek("GET", yol, "bozuk.token", ""), 401)
	}
}

func TestAdminYetkisi(t *testing.T) {
	o := kur(t)
	ali := o.giris("ali@test.com")
	admin := o.giris("admin@test.com")
	for _, d := range []struct{ metot, yol, govde string }{
		{"POST", "/api/v1/cards/batch", `[]`},
		{"GET", "/api/v1/users", ""},
		{"POST", "/api/v1/users", `{"eposta":"yeni@test.com","sifre":"sifre-12345","rol":"admin"}`},
		{"GET", "/api/v1/admin/reconciliation", ""},
	} {
		o.kod(o.istek(d.metot, d.yol, ali, d.govde), 403)
	}

	o.kod(o.istek("POST", "/api/v1/users", admin, `{"eposta":"veli@test.com","sifre":"sifre-12345","rol":"kullanici"}`), 201)
	o.kod(o.istek("POST", "/api/v1/users", admin, `{"eposta":"veli@test.com","sifre":"sifre-12345","rol":"kullanici"}`), 409)
	o.kod(o.istek("POST", "/api/v1/users", admin, `{"eposta":"kisa@test.com","sifre":"123","rol":"kullanici"}`), 400)

	y := o.istek("POST", "/api/v1/cards/batch", admin, `[{"kart_no":"5555-6666","bakiye":"10.50","sahip_eposta":"veli@test.com"},{"kart_no":"1111-2222","bakiye":1}]`)
	o.kod(y, 200)
	if y.veri["eklenen_sayi"] != 1.0 || y.veri["atlanan_sayi"] != 1.0 {
		t.Fatalf("%v", y.veri)
	}
	y = o.istek("GET", "/api/v1/admin/reconciliation", admin, "")
	o.kod(y, 200)
	if y.veri["tutarli"] != true {
		t.Fatalf("%v", y.veri)
	}
}

func TestKartlarMaskeli(t *testing.T) {
	o := kur(t)
	y := o.istek("GET", "/api/v1/cards", o.giris("ali@test.com"), "")
	o.kod(y, 200)
	if strings.Contains(y.Body.String(), "1111-2222") || !strings.Contains(y.Body.String(), "•••• 2222") {
		t.Fatalf("kart numarası maskelenmemiş: %s", y.Body)
	}
	if strings.Contains(y.Body.String(), "4444") {
		t.Fatal("başkasının kartı listelenmemeli")
	}
}

func TestOdemeVeIadeAkisi(t *testing.T) {
	o := kur(t)
	ali := o.giris("ali@test.com")

	y := o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"1111-2222","tutar":"250.50","aciklama":"kitap"}`)
	o.kod(y, 201)
	if y.veri["kalan_bakiye"] != 749.5 {
		t.Fatalf("%v", y.veri)
	}
	ref := y.veri["odeme"].(map[string]any)["id"].(string)

	// Eski API'deki istemci bakiyesi kabul edilmez.
	o.kod(o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"1111-2222","bakiye":99999,"tutar":1}`), 400)
	// Başkasının kartı bulunamaz.
	o.kod(o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"3333-4444","tutar":1}`), 404)

	y = o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"1111-2222","tutar":5000}`)
	o.kod(y, 402)
	if y.veri["kod"] != "yetersiz_bakiye" || y.veri["odeme"].(map[string]any)["durum"] != "reddedildi" {
		t.Fatalf("%v", y.veri)
	}

	o.kod(o.istek("GET", "/api/v1/payments/"+ref, ali, ""), 200)
	o.kod(o.istek("GET", "/api/v1/payments/"+ref, o.giris("ayse@test.com"), ""), 404)

	y = o.istek("POST", "/api/v1/payments/"+ref+"/refunds", ali, `{"tutar":50}`)
	o.kod(y, 200)
	if y.veri["odeme"].(map[string]any)["durum"] != "kismi_iade" || y.veri["kalan_bakiye"] != 799.5 {
		t.Fatalf("%v", y.veri)
	}
	y = o.istek("POST", "/api/v1/payments/"+ref+"/refunds", ali, "") // gövdesiz: kalanın tamamı
	o.kod(y, 200)
	if y.veri["odeme"].(map[string]any)["durum"] != "iade_edildi" || y.veri["kalan_bakiye"] != 1000.0 {
		t.Fatalf("%v", y.veri)
	}
	o.kod(o.istek("POST", "/api/v1/payments/"+ref+"/refunds", ali, ""), 409)

	y = o.istek("GET", "/api/v1/payments?limit=1", ali, "")
	o.kod(y, 200)
	if len(y.veri["odemeler"].([]any)) != 1 || y.veri["sonraki_imlec"] == nil {
		t.Fatalf("%v", y.veri)
	}
	o.kod(o.istek("GET", "/api/v1/payments?limit=abc", ali, ""), 400)
	o.kod(o.istek("GET", "/api/v1/payments?once=yok", ali, ""), 400)
}

func TestIdempotency(t *testing.T) {
	o := kur(t)
	ali := o.giris("ali@test.com")
	govde := `{"kart_no":"1111-2222","tutar":100}`

	y1 := o.istek("POST", "/api/v1/payments", ali, govde, IdempotencyBasligi, "anahtar-1")
	o.kod(y1, 201)
	y2 := o.istek("POST", "/api/v1/payments", ali, govde, IdempotencyBasligi, "anahtar-1")
	o.kod(y2, 201)
	if y2.Header().Get("Idempotent-Replayed") != "true" || y1.Body.String() != y2.Body.String() {
		t.Fatalf("aynı yanıt dönmeli:\n%s\n%s", y1.Body, y2.Body)
	}

	// Aynı anahtar, farklı gövde -> 422
	o.kod(o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"1111-2222","tutar":200}`, IdempotencyBasligi, "anahtar-1"), 422)

	// Başka kullanıcının aynı anahtarı çakışmaz.
	o.kod(o.istek("POST", "/api/v1/payments", o.giris("ayse@test.com"), `{"kart_no":"3333-4444","tutar":100}`, IdempotencyBasligi, "anahtar-1"), 201)

	// Kalan bakiye yalnızca bir kez düşmüş olmalı.
	y := o.istek("GET", "/api/v1/cards", ali, "")
	if b := y.veri["kartlar"].([]any)[0].(map[string]any)["bakiye"]; b != 900.0 {
		t.Fatalf("bakiye %v, beklenen 900", b)
	}

	// 4xx yanıtlar da saklanır (aynı hatalı istek aynı sonucu alır).
	o.kod(o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"1111-2222","tutar":-1}`, IdempotencyBasligi, "anahtar-2"), 400)
	y = o.istek("POST", "/api/v1/payments", ali, `{"kart_no":"1111-2222","tutar":-1}`, IdempotencyBasligi, "anahtar-2")
	o.kod(y, 400)
	if y.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatal("hata yanıtı tekrar oynatılmalı")
	}

	o.kod(o.istek("POST", "/api/v1/payments", ali, govde, IdempotencyBasligi, strings.Repeat("x", 256)), 400)
}

// Aynı anahtarla eşzamanlı 10 istek: yalnızca bir ödeme yapılmalı.
func TestIdempotencyEszamanli(t *testing.T) {
	o := kur(t)
	ali := o.giris("ali@test.com")
	var wg sync.WaitGroup
	kodlar := make([]int, 10)
	for i := range kodlar {
		wg.Go(func() {
			r := httptest.NewRequest("POST", "/api/v1/payments", strings.NewReader(`{"kart_no":"1111-2222","tutar":100}`))
			r.RemoteAddr = "10.0.0.1:1"
			r.Header.Set("Authorization", "Bearer "+ali)
			r.Header.Set(IdempotencyBasligi, "esz-1")
			w := httptest.NewRecorder()
			o.h.ServeHTTP(w, r)
			kodlar[i] = w.Code
		})
	}
	wg.Wait()
	for _, k := range kodlar {
		if k != 201 && k != 409 {
			t.Fatalf("beklenmeyen kod %d: %v", k, kodlar)
		}
	}
	var sayi int64
	o.s.DB.Model(&veri.Odeme{}).Count(&sayi)
	if sayi != 1 {
		t.Fatalf("%d ödeme oluştu, beklenen 1 (kodlar %v)", sayi, kodlar)
	}
}

func TestGuvenlikBasliklariVeStatik(t *testing.T) {
	o := kur(t)
	y := o.istek("GET", "/", "", "")
	o.kod(y, 200)
	csp := y.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || y.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("güvenlik başlıkları eksik: %v", y.Header())
	}
	if y.Header().Get("X-Request-ID") == "" {
		t.Fatal("X-Request-ID eksik")
	}

	y = o.istek("GET", "/api/v1/yok", "", "")
	o.kod(y, 404)
	if y.veri["kod"] != "bulunamadi" || y.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("API 404 JSON olmalı: %s", y.Body)
	}
	o.kod(o.istek("GET", "/healthz", "", ""), 200)
	o.kod(o.istek("GET", "/readyz", "", ""), 200)
	o.kod(o.istek("GET", "/openapi.yaml", "", ""), 200)
}

func TestMetrikler(t *testing.T) {
	o := kur(t)
	ali := o.giris("ali@test.com")
	o.istek("GET", "/api/v1/payments/odm_yok", ali, "")
	y := o.istek("GET", "/metrics", "", "")
	o.kod(y, 200)
	// Rota deseni kullanılmalı; ham ödeme ID'si metriklere girmemeli.
	if !strings.Contains(y.Body.String(), `rota="GET /api/v1/payments/{id}"`) || strings.Contains(y.Body.String(), "odm_yok") {
		t.Fatalf("metrikler:\n%s", y.Body)
	}
}

func TestProxyIP(t *testing.T) {
	s := &Sunucu{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2")
	if ip := s.istemciIP(r); ip != "10.0.0.1" {
		t.Fatalf("proxy kapalıyken başlık yok sayılmalı: %s", ip)
	}
	s.ProxyArkasinda = true
	if ip := s.istemciIP(r); ip != "2.2.2.2" {
		t.Fatalf("son XFF adresi kullanılmalı: %s", ip)
	}
}

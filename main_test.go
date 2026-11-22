package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"go-ilk-proje/payment"
)

const testAnahtar = "test-anahtari-en-az-otuz-iki-karakter-uzunlugunda"

func testApp(t *testing.T) (*App, http.Handler) {
	t.Helper()
	servis, err := payment.VeritabaniBaglan(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db, err := servis.DB.DB(); err == nil {
			db.Close()
		}
	})
	auth, err := payment.YeniAuth(servis.DB, testAnahtar)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.KullaniciOlustur("admin@test.com", "admin-sifre-1", payment.RolAdmin); err != nil {
		t.Fatal(err)
	}
	if err := auth.KullaniciOlustur("kullanici@test.com", "kullanici-sifre-1", payment.RolKullanici); err != nil {
		t.Fatal(err)
	}
	if _, err := servis.TopluKartEkle([]payment.KrediKarti{{KartNo: "1111-2222", Bakiye: 100000}}); err != nil {
		t.Fatal(err)
	}
	app := &App{servis: servis, auth: auth}
	return app, app.routes()
}

func istek(t *testing.T, h http.Handler, metot, yol, token, govde string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(metot, yol, strings.NewReader(govde))
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var veri map[string]any
	json.Unmarshal(w.Body.Bytes(), &veri)
	return w, veri
}

func girisYap(t *testing.T, h http.Handler, eposta, sifre string) string {
	t.Helper()
	w, veri := istek(t, h, "POST", "/api/v1/login", "", `{"eposta":"`+eposta+`","sifre":"`+sifre+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("giriş başarısız: %d %s", w.Code, w.Body)
	}
	return veri["token"].(string)
}

func TestLogin(t *testing.T) {
	_, h := testApp(t)
	girisYap(t, h, "admin@test.com", "admin-sifre-1")

	for _, govde := range []string{
		`{"eposta":"admin@test.com","sifre":"yanlis"}`,
		`{"eposta":"yok@test.com","sifre":"admin-sifre-1"}`,
		`{"eposta":"enes@test.com","sifre":"123456"}`,
	} {
		if w, _ := istek(t, h, "POST", "/api/v1/login", "", govde); w.Code != http.StatusUnauthorized {
			t.Errorf("%s -> %d, beklenen 401", govde, w.Code)
		}
	}
}

func TestOdemeTokenGerekli(t *testing.T) {
	_, h := testApp(t)
	for _, token := range []string{"", "bozuk.token.degeri"} {
		w, _ := istek(t, h, "POST", "/api/v1/pay", token, `{"kart_no":"1111-2222","tutar":10}`)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("token %q -> %d", token, w.Code)
		}
	}
}

func TestOdemeIstemciBakiyesiReddedilir(t *testing.T) {
	_, h := testApp(t)
	token := girisYap(t, h, "kullanici@test.com", "kullanici-sifre-1")

	// Eski API'deki "bakiye" alanı artık kabul edilmiyor.
	w, _ := istek(t, h, "POST", "/api/v1/pay", token, `{"kart_no":"9999-8888","bakiye":1000000,"tutar":500}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bakiye alanı reddedilmeli, kod %d", w.Code)
	}
	// Var olmayan kart otomatik oluşturulmuyor.
	w, _ = istek(t, h, "POST", "/api/v1/pay", token, `{"kart_no":"9999-8888","tutar":500}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bilinmeyen kart -> %d, beklenen 404", w.Code)
	}
}

func TestOdemeAkisi(t *testing.T) {
	_, h := testApp(t)
	token := girisYap(t, h, "kullanici@test.com", "kullanici-sifre-1")

	w, veri := istek(t, h, "POST", "/api/v1/pay", token, `{"kart_no":"1111-2222","tutar":"250.50"}`)
	if w.Code != http.StatusOK || veri["kalan_bakiye"] != 749.5 {
		t.Fatalf("%d %v", w.Code, veri)
	}
	w, _ = istek(t, h, "POST", "/api/v1/pay", token, `{"kart_no":"1111-2222","tutar":-100}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("negatif tutar -> %d", w.Code)
	}
	w, _ = istek(t, h, "POST", "/api/v1/pay", token, `{"kart_no":"1111-2222","tutar":5000}`)
	if w.Code != http.StatusPaymentRequired {
		t.Fatalf("yetersiz bakiye -> %d", w.Code)
	}
	w, _ = istek(t, h, "GET", "/api/v1/transactions", token, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tutar":250.50`) {
		t.Fatalf("işlemler: %d %s", w.Code, w.Body)
	}
}

func TestTopluKartSadeceAdmin(t *testing.T) {
	_, h := testApp(t)
	govde := `[{"kart_no":"3333-4444","bakiye":2000}]`

	kullanici := girisYap(t, h, "kullanici@test.com", "kullanici-sifre-1")
	if w, _ := istek(t, h, "POST", "/api/v1/cards/batch", kullanici, govde); w.Code != http.StatusForbidden {
		t.Fatalf("normal kullanıcı -> %d, beklenen 403", w.Code)
	}

	admin := girisYap(t, h, "admin@test.com", "admin-sifre-1")
	w, veri := istek(t, h, "POST", "/api/v1/cards/batch", admin, govde)
	if w.Code != http.StatusOK || veri["eklenen_sayi"] != float64(1) {
		t.Fatalf("admin -> %d %v", w.Code, veri)
	}
}

func TestZayifJWTAnahtariReddedilir(t *testing.T) {
	if _, err := payment.YeniAuth(nil, "gizli_go_anahtari_123!"); err == nil {
		t.Fatal("kısa anahtar reddedilmeli")
	}
}

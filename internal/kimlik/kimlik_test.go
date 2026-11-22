package kimlik

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri/veritest"
)

const anahtar = "test-anahtari-en-az-otuz-iki-karakter-uzunlugunda"

func kur(t *testing.T) *Servis {
	t.Helper()
	s, err := Yeni(veritest.Ac(t), anahtar, bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestKisaAnahtarReddedilir(t *testing.T) {
	if _, err := Yeni(nil, "gizli_go_anahtari_123!", 0); !errors.Is(err, ErrZayifJWTAnahtar) {
		t.Fatal(err)
	}
}

func TestKullaniciOlusturDogrulama(t *testing.T) {
	s := kur(t)
	durumlar := []struct {
		eposta, sifre, rol string
		err                error
	}{
		{"gecersiz", "sifre-12345", veri.RolKullanici, ErrGecersizEposta},
		{"Ad Soyad <a@b.com>", "sifre-12345", veri.RolKullanici, ErrGecersizEposta},
		{"a@b.com", "kisa", veri.RolKullanici, ErrGecersizSifre},
		{"a@b.com", strings.Repeat("x", 73), veri.RolKullanici, ErrGecersizSifre},
		{"a@b.com", "sifre-12345", "root", ErrGecersizRol},
	}
	for _, d := range durumlar {
		if _, err := s.KullaniciOlustur(d.eposta, d.sifre, d.rol); !errors.Is(err, d.err) {
			t.Errorf("%+v: err=%v", d, err)
		}
	}
	if _, err := s.KullaniciOlustur(" A@B.com ", "sifre-12345", veri.RolKullanici); err != nil {
		t.Fatal(err)
	}
	if _, err := s.KullaniciOlustur("a@b.com", "sifre-12345", veri.RolKullanici); !errors.Is(err, ErrEpostaKullanimda) {
		t.Fatalf("büyük/küçük harf farkı aynı e-posta sayılmalı: %v", err)
	}
}

func TestGirisVeToken(t *testing.T) {
	s := kur(t)
	k, _ := s.KullaniciOlustur("ali@test.com", "sifre-12345", veri.RolKullanici)

	token, kim, err := s.GirisYap("Ali@Test.com", "sifre-12345")
	if err != nil || kim.ID != k.ID {
		t.Fatalf("%v %+v", err, kim)
	}
	dogrulanan, err := s.TokenDogrula(token)
	if err != nil || dogrulanan != kim {
		t.Fatalf("%v %+v", err, dogrulanan)
	}
	if _, _, err := s.GirisYap("ali@test.com", "yanlis"); !errors.Is(err, ErrHataliGiris) {
		t.Fatal(err)
	}
	if _, _, err := s.GirisYap("yok@test.com", "sifre-12345"); !errors.Is(err, ErrHataliGiris) {
		t.Fatal(err)
	}
}

func TestSahteTokenlarReddedilir(t *testing.T) {
	s := kur(t)
	imzala := func(m jwt.SigningMethod, anahtar any, c jwt.Claims) string {
		str, err := jwt.NewWithClaims(m, c).SignedString(anahtar)
		if err != nil {
			t.Fatal(err)
		}
		return str
	}
	gecerli := jwt.MapClaims{"sub": "1", "rol": "admin", "exp": time.Now().Add(time.Hour).Unix()}
	durumlar := map[string]string{
		"başka anahtar": imzala(jwt.SigningMethodHS256, []byte(strings.Repeat("b", 40)), gecerli),
		"alg none":      imzala(jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, gecerli),
		"HS512":         imzala(jwt.SigningMethodHS512, []byte(anahtar), gecerli),
		"süresi dolmuş": imzala(jwt.SigningMethodHS256, []byte(anahtar), jwt.MapClaims{"sub": "1", "exp": time.Now().Add(-time.Minute).Unix()}),
		"exp yok":       imzala(jwt.SigningMethodHS256, []byte(anahtar), jwt.MapClaims{"sub": "1"}),
		"sub yok":       imzala(jwt.SigningMethodHS256, []byte(anahtar), jwt.MapClaims{"exp": time.Now().Add(time.Hour).Unix()}),
		"bozuk":         "a.b.c",
	}
	for ad, tok := range durumlar {
		if _, err := s.TokenDogrula(tok); !errors.Is(err, ErrGecersizToken) {
			t.Errorf("%s kabul edildi", ad)
		}
	}
}

func TestAdminYoksaOlustur(t *testing.T) {
	s := kur(t)
	if _, err := s.AdminYoksaOlustur("", ""); err == nil {
		t.Fatal("boş veritabanında admin bilgisi zorunlu olmalı")
	}
	if ok, err := s.AdminYoksaOlustur("admin@test.com", "sifre-12345"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := s.AdminYoksaOlustur("", ""); ok || err != nil {
		t.Fatal("kullanıcı varsa bir şey yapmamalı", ok, err)
	}
}

package ayarlar

import (
	"strings"
	"testing"
)

func ortam(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const anahtar = "0123456789abcdef0123456789abcdef"

func TestVarsayilanlar(t *testing.T) {
	a, err := Oku(ortam(map[string]string{"JWT_SECRET": anahtar}))
	if err != nil {
		t.Fatal(err)
	}
	if a.Port != "8080" || a.DBSurucu != "sqlite" || a.DBAdres != "odeme_sistemi.db" || a.ProxyArkasinda || a.LogFormati != "text" {
		t.Fatalf("%+v", a)
	}
}

func TestEskiDBPathOkunur(t *testing.T) {
	a, _ := Oku(ortam(map[string]string{"JWT_SECRET": anahtar, "DB_PATH": "/veri/eski.db"}))
	if a.DBAdres != "/veri/eski.db" {
		t.Fatal(a.DBAdres)
	}
}

func TestHatalarBirlikteDoner(t *testing.T) {
	_, err := Oku(ortam(map[string]string{
		"JWT_SECRET": "kisa", "PORT": "abc", "DB_DRIVER": "postgres",
		"WEBHOOK_URL": "ftp://x", "TRUST_PROXY": "belki", "LOG_FORMAT": "xml",
	}))
	if err == nil {
		t.Fatal("hata bekleniyordu")
	}
	for _, parca := range []string{"JWT_SECRET", "PORT", "DATABASE_URL", "WEBHOOK_URL", "WEBHOOK_SECRET", "TRUST_PROXY", "LOG_FORMAT"} {
		if !strings.Contains(err.Error(), parca) {
			t.Errorf("hata mesajında %s yok: %v", parca, err)
		}
	}
}

func TestPostgres(t *testing.T) {
	a, err := Oku(ortam(map[string]string{
		"JWT_SECRET": anahtar, "DB_DRIVER": "postgres", "DATABASE_URL": "postgres://u:p@db/odeme",
		"WEBHOOK_URL": "https://ornek.com/hook", "WEBHOOK_SECRET": anahtar, "TRUST_PROXY": "true",
	}))
	if err != nil || a.DBAdres != "postgres://u:p@db/odeme" || !a.ProxyArkasinda {
		t.Fatalf("%+v %v", a, err)
	}
}

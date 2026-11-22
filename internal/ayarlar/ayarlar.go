// Package ayarlar, uygulama ayarlarını ortam değişkenlerinden okur ve doğrular.
package ayarlar

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
)

type Ayarlar struct {
	Port           string
	DBSurucu       string // sqlite | postgres
	DBAdres        string // SQLite dosya yolu veya PostgreSQL bağlantı adresi
	JWTAnahtar     string
	AdminEposta    string
	AdminSifre     string
	WebhookURL     string
	WebhookGizli   string
	ProxyArkasinda bool
	LogFormati     string // text | json
}

// Oku, ayarları verilen fonksiyondan (genelde os.Getenv) okur. Tüm hatalar birlikte döner.
func Oku(getenv func(string) string) (Ayarlar, error) {
	var hatalar []error
	ortam := func(ad, varsayilan string) string {
		if v := strings.TrimSpace(getenv(ad)); v != "" {
			return v
		}
		return varsayilan
	}

	a := Ayarlar{
		Port:         ortam("PORT", "8080"),
		DBSurucu:     ortam("DB_DRIVER", "sqlite"),
		JWTAnahtar:   getenv("JWT_SECRET"),
		AdminEposta:  getenv("ADMIN_EMAIL"),
		AdminSifre:   getenv("ADMIN_PASSWORD"),
		WebhookURL:   ortam("WEBHOOK_URL", ""),
		WebhookGizli: getenv("WEBHOOK_SECRET"),
		LogFormati:   ortam("LOG_FORMAT", "text"),
	}

	if p, err := strconv.Atoi(a.Port); err != nil || p < 1 || p > 65535 {
		hatalar = append(hatalar, fmt.Errorf("PORT geçersiz: %q", a.Port))
	}

	switch a.DBSurucu {
	case "sqlite":
		// DB_PATH eski sürümle uyumluluk için hâlâ okunur.
		a.DBAdres = ortam("DATABASE_URL", ortam("DB_PATH", "odeme_sistemi.db"))
	case "postgres":
		a.DBAdres = ortam("DATABASE_URL", "")
		if a.DBAdres == "" {
			hatalar = append(hatalar, errors.New("DB_DRIVER=postgres için DATABASE_URL gerekli"))
		}
	default:
		hatalar = append(hatalar, fmt.Errorf("DB_DRIVER 'sqlite' veya 'postgres' olmalı, %q verildi", a.DBSurucu))
	}

	if len(a.JWTAnahtar) < kimlik.MinJWTAnahtarBoyu {
		hatalar = append(hatalar, kimlik.ErrZayifJWTAnahtar)
	}

	if a.WebhookURL != "" {
		u, err := url.Parse(a.WebhookURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			hatalar = append(hatalar, fmt.Errorf("WEBHOOK_URL geçersiz: %q", a.WebhookURL))
		}
		if len(a.WebhookGizli) < 32 {
			hatalar = append(hatalar, errors.New("WEBHOOK_URL ayarlıysa WEBHOOK_SECRET en az 32 karakter olmalı"))
		}
	}

	switch v := strings.ToLower(ortam("TRUST_PROXY", "false")); v {
	case "1", "true", "evet":
		a.ProxyArkasinda = true
	case "0", "false", "hayir":
	default:
		hatalar = append(hatalar, fmt.Errorf("TRUST_PROXY true/false olmalı, %q verildi", v))
	}

	if a.LogFormati != "text" && a.LogFormati != "json" {
		hatalar = append(hatalar, fmt.Errorf("LOG_FORMAT 'text' veya 'json' olmalı, %q verildi", a.LogFormati))
	}

	return a, errors.Join(hatalar...)
}

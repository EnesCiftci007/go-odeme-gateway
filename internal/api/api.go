// Package api, HTTP katmanını içerir: yönlendirme, handler'lar ve ara katmanlar.
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/time/rate"
	"gorm.io/gorm"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/odeme"
)

const maksIstekBoyutu = 1 << 20 // 1 MB

type Sunucu struct {
	DB      *gorm.DB
	Kimlik  *kimlik.Servis
	Odeme   *odeme.Servis
	Metrik  *Metrikler
	Statik  fs.FS  // ön yüz dosyaları
	OpenAPI []byte // API dokümanı
	// ProxyArkasinda true ise istemci IP'si X-Forwarded-For başlığından okunur.
	// Yalnızca güvenilir bir ters proxy arkasında açılmalıdır.
	ProxyArkasinda bool

	girisLimiti *limitci
	apiLimiti   *limitci
}

// Handler, tüm rotaları ve ara katmanları kurulmuş HTTP handler'ını döndürür.
func (s *Sunucu) Handler() http.Handler {
	if s.Metrik == nil {
		s.Metrik = YeniMetrikler()
	}
	// Giriş: IP başına dakikada ~5 deneme (kaba kuvvet saldırılarına karşı).
	s.girisLimiti = yeniLimitci(rate.Every(12*time.Second), 5)
	// Diğer API çağrıları: IP başına saniyede 30 istek.
	s.apiLimiti = yeniLimitci(30, 60)

	mux := http.NewServeMux()
	api := func(desen string, h http.HandlerFunc) {
		mux.Handle(desen, s.limitle(s.apiLimiti, h))
	}

	mux.Handle("POST /api/v1/auth/login", s.limitle(s.girisLimiti, s.girisHandler))
	api("GET /api/v1/me", s.oturumGerekli(s.benHandler))

	api("GET /api/v1/cards", s.oturumGerekli(s.kartlarHandler))
	api("POST /api/v1/cards/batch", s.adminGerekli(s.topluKartHandler))

	api("POST /api/v1/payments", s.oturumGerekli(s.idempotent(s.odemeHandler)))
	api("GET /api/v1/payments", s.oturumGerekli(s.odemelerHandler))
	api("GET /api/v1/payments/{id}", s.oturumGerekli(s.odemeGetirHandler))
	api("POST /api/v1/payments/{id}/refunds", s.oturumGerekli(s.idempotent(s.iadeHandler)))

	api("GET /api/v1/users", s.adminGerekli(s.kullanicilarHandler))
	api("POST /api/v1/users", s.adminGerekli(s.kullaniciOlusturHandler))
	api("GET /api/v1/admin/reconciliation", s.adminGerekli(s.mutabakatHandler))

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		hataYaz(w, http.StatusNotFound, "bulunamadi", "Böyle bir API yolu yok")
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		jsonYaz(w, http.StatusOK, map[string]string{"durum": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.hazirHandler)
	mux.Handle("GET /metrics", s.Metrik.Handler())
	mux.HandleFunc("GET /openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Write(s.OpenAPI)
	})
	if s.Statik != nil {
		dosyalar := http.FileServerFS(s.Statik)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
				return
			}
			dosyalar.ServeHTTP(w, r)
		})
	}

	var h http.Handler = mux
	h = s.Metrik.ara(h)
	h = guvenlikBasliklari(h)
	h = s.logla(h)
	h = kurtar(h)
	h = istekKimligi(h)
	return h
}

// ---- JSON yardımcıları ----

func jsonYaz(w http.ResponseWriter, durum int, veri any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(durum)
	if err := json.NewEncoder(w).Encode(veri); err != nil {
		slog.Error("yanıt yazılamadı", "hata", err)
	}
}

func hataYaz(w http.ResponseWriter, durum int, kod, mesaj string) {
	jsonYaz(w, durum, map[string]any{"basarili": false, "kod": kod, "mesaj": mesaj})
}

func jsonOku(w http.ResponseWriter, r *http.Request, hedef any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maksIstekBoyutu)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(hedef); err != nil {
		hataYaz(w, http.StatusBadRequest, "gecersiz_json", "Geçersiz JSON: "+err.Error())
		return false
	}
	if dec.More() {
		hataYaz(w, http.StatusBadRequest, "gecersiz_json", "Geçersiz JSON: gövdede birden fazla değer var")
		return false
	}
	return true
}

// isHatasiYaz, servis katmanından gelen hatayı uygun HTTP durumuna çevirir.
// Beklenmeyen hatalar loglanır ve istemciye ayrıntı verilmez.
func isHatasiYaz(w http.ResponseWriter, r *http.Request, err error) {
	var h *odeme.Hata
	if errors.As(err, &h) {
		durum := http.StatusBadRequest
		switch h.Kod {
		case odeme.ErrKartBulunamadi.Kod, odeme.ErrOdemeBulunamadi.Kod:
			durum = http.StatusNotFound
		case odeme.ErrYetersizBakiye.Kod:
			durum = http.StatusPaymentRequired
		case odeme.ErrIadeEdilemez.Kod, odeme.ErrIadeTutariFazla.Kod:
			durum = http.StatusConflict
		}
		hataYaz(w, durum, h.Kod, h.Mesaj)
		return
	}
	for _, e := range []error{kimlik.ErrGecersizEposta, kimlik.ErrGecersizSifre, kimlik.ErrGecersizRol} {
		if errors.Is(err, e) {
			hataYaz(w, http.StatusBadRequest, "gecersiz_veri", err.Error())
			return
		}
	}
	if errors.Is(err, kimlik.ErrEpostaKullanimda) {
		hataYaz(w, http.StatusConflict, "eposta_kullanimda", err.Error())
		return
	}
	slog.Error("beklenmeyen hata", "hata", err, "istek_id", istekKimligiAl(r.Context()))
	hataYaz(w, http.StatusInternalServerError, "sunucu_hatasi", "Sunucu hatası")
}

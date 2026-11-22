package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"go-ilk-proje/payment"
)

const maksIstekBoyutu = 1 << 20 // 1 MB

type LoginIstek struct {
	Eposta string `json:"eposta"`
	Sifre  string `json:"sifre"`
}

// OdemeIstek'te bakiye alanı yoktur: bakiye yalnızca veritabanından gelir.
type OdemeIstek struct {
	KartNo string        `json:"kart_no"`
	Tutar  payment.Kurus `json:"tutar"`
}

type App struct {
	servis *payment.Servis
	auth   *payment.Auth
}

type kimlikAnahtari struct{}

func jsonYaz(w http.ResponseWriter, durum int, veri any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(durum)
	if err := json.NewEncoder(w).Encode(veri); err != nil {
		slog.Error("yanıt yazılamadı", "hata", err)
	}
}

func hataYaz(w http.ResponseWriter, durum int, mesaj string) {
	jsonYaz(w, durum, map[string]any{"basarili": false, "mesaj": mesaj})
}

func jsonOku(w http.ResponseWriter, r *http.Request, hedef any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maksIstekBoyutu)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(hedef); err != nil {
		hataYaz(w, http.StatusBadRequest, "Geçersiz JSON: "+err.Error())
		return false
	}
	return true
}

func (a *App) loginHandler(w http.ResponseWriter, r *http.Request) {
	var istek LoginIstek
	if !jsonOku(w, r, &istek) {
		return
	}
	if istek.Eposta == "" || istek.Sifre == "" {
		hataYaz(w, http.StatusBadRequest, "E-posta ve şifre zorunlu")
		return
	}

	token, err := a.auth.GirisYap(istek.Eposta, istek.Sifre)
	if errors.Is(err, payment.ErrHataliGiris) {
		hataYaz(w, http.StatusUnauthorized, "Hatalı e-posta veya şifre")
		return
	}
	if err != nil {
		slog.Error("giriş hatası", "hata", err)
		hataYaz(w, http.StatusInternalServerError, "Sunucu hatası")
		return
	}
	jsonYaz(w, http.StatusOK, map[string]any{"basarili": true, "token": token, "mesaj": "Giriş başarılı"})
}

func (a *App) odemeHandler(w http.ResponseWriter, r *http.Request) {
	var istek OdemeIstek
	if !jsonOku(w, r, &istek) {
		return
	}

	kalan, err := a.servis.OdemeYap(istek.KartNo, istek.Tutar)
	switch {
	case err == nil:
		jsonYaz(w, http.StatusOK, map[string]any{
			"basarili": true, "mesaj": "Ödeme başarıyla tamamlandı", "kalan_bakiye": kalan,
		})
	case errors.Is(err, payment.ErrYetersizBakiye):
		jsonYaz(w, http.StatusPaymentRequired, map[string]any{
			"basarili": false, "mesaj": err.Error(), "kalan_bakiye": kalan,
		})
	case errors.Is(err, payment.ErrKartBulunamadi):
		hataYaz(w, http.StatusNotFound, "Kart bulunamadı")
	case errors.Is(err, payment.ErrGecersizKartNo), errors.Is(err, payment.ErrGecersizTutar):
		hataYaz(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("ödeme hatası", "hata", err)
		hataYaz(w, http.StatusInternalServerError, "Sunucu hatası")
	}
}

func (a *App) islemlerHandler(w http.ResponseWriter, r *http.Request) {
	islemler, err := a.servis.SonIslemler(10)
	if err != nil {
		slog.Error("işlemler okunamadı", "hata", err)
		hataYaz(w, http.StatusInternalServerError, "Sunucu hatası")
		return
	}
	jsonYaz(w, http.StatusOK, islemler)
}

// topluKartHandler, kartları başlangıç bakiyesiyle ekler. Yalnızca admin kullanabilir.
func (a *App) topluKartHandler(w http.ResponseWriter, r *http.Request) {
	var kartlar []payment.KrediKarti
	if !jsonOku(w, r, &kartlar) {
		return
	}

	eklenen, err := a.servis.TopluKartEkle(kartlar)
	if errors.Is(err, payment.ErrGecersizKartNo) || errors.Is(err, payment.ErrGecersizTutar) ||
		errors.Is(err, payment.ErrCokFazlaKart) {
		hataYaz(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		slog.Error("toplu kart ekleme hatası", "hata", err)
		hataYaz(w, http.StatusInternalServerError, "Sunucu hatası")
		return
	}
	jsonYaz(w, http.StatusOK, map[string]any{
		"basarili":     true,
		"eklenen_sayi": eklenen,
		"mesaj":        fmt.Sprintf("%d yeni kart veritabanına eklendi", eklenen),
	})
}

func (a *App) jwtGerekli(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			hataYaz(w, http.StatusUnauthorized, "Authorization başlığı eksik veya hatalı")
			return
		}
		kimlik, err := a.auth.TokenDogrula(tokenStr)
		if err != nil {
			hataYaz(w, http.StatusUnauthorized, "Geçersiz veya süresi dolmuş token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), kimlikAnahtari{}, kimlik)))
	}
}

func (a *App) adminGerekli(next http.HandlerFunc) http.HandlerFunc {
	return a.jwtGerekli(func(w http.ResponseWriter, r *http.Request) {
		kimlik, _ := r.Context().Value(kimlikAnahtari{}).(payment.Kimlik)
		if kimlik.Rol != payment.RolAdmin {
			hataYaz(w, http.StatusForbidden, "Bu işlem için admin yetkisi gerekli")
			return
		}
		next(w, r)
	})
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/login", a.loginHandler)
	mux.HandleFunc("POST /api/v1/pay", a.jwtGerekli(a.odemeHandler))
	mux.HandleFunc("GET /api/v1/transactions", a.jwtGerekli(a.islemlerHandler))
	mux.HandleFunc("POST /api/v1/cards/batch", a.adminGerekli(a.topluKartHandler))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./templates/index.html")
	})
	return mux
}

func ortamDegiskeni(ad, varsayilan string) string {
	if v := os.Getenv(ad); v != "" {
		return v
	}
	return varsayilan
}

func main() {
	if err := calistir(); err != nil {
		slog.Error("sunucu durdu", "hata", err)
		os.Exit(1)
	}
}

func calistir() error {
	servis, err := payment.VeritabaniBaglan(ortamDegiskeni("DB_PATH", "odeme_sistemi.db"))
	if err != nil {
		return fmt.Errorf("veritabanına bağlanılamadı: %w", err)
	}

	auth, err := payment.YeniAuth(servis.DB, os.Getenv("JWT_SECRET"))
	if err != nil {
		return err
	}

	olustu, err := auth.AdminYoksaOlustur(os.Getenv("ADMIN_EMAIL"), os.Getenv("ADMIN_PASSWORD"))
	if err != nil {
		return fmt.Errorf("admin hesabı oluşturulamadı: %w", err)
	}
	if olustu {
		slog.Info("ilk admin hesabı oluşturuldu", "eposta", os.Getenv("ADMIN_EMAIL"))
	}

	app := &App{servis: servis, auth: auth}
	srv := &http.Server{
		Addr:              ":" + ortamDegiskeni("PORT", "8080"),
		Handler:           app.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	slog.Info("sunucu başlatılıyor", "adres", srv.Addr)
	return srv.ListenAndServe()
}

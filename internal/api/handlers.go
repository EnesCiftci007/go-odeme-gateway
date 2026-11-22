package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/odeme"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/para"
)

func (s *Sunucu) girisHandler(w http.ResponseWriter, r *http.Request) {
	var istek struct {
		Eposta string `json:"eposta"`
		Sifre  string `json:"sifre"`
	}
	if !jsonOku(w, r, &istek) {
		return
	}
	if istek.Eposta == "" || istek.Sifre == "" {
		hataYaz(w, http.StatusBadRequest, "gecersiz_veri", "E-posta ve şifre zorunlu")
		return
	}
	token, kim, err := s.Kimlik.GirisYap(istek.Eposta, istek.Sifre)
	if errors.Is(err, kimlik.ErrHataliGiris) {
		hataYaz(w, http.StatusUnauthorized, "hatali_giris", "Hatalı e-posta veya şifre")
		return
	}
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, map[string]any{"basarili": true, "token": token, "kullanici": kim})
}

func (s *Sunucu) benHandler(w http.ResponseWriter, r *http.Request) {
	jsonYaz(w, http.StatusOK, kimlikAl(r))
}

func (s *Sunucu) kartlarHandler(w http.ResponseWriter, r *http.Request) {
	kartlar, err := s.Odeme.Kartlar(kimlikAl(r))
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, map[string]any{"kartlar": kartlar})
}

func (s *Sunucu) topluKartHandler(w http.ResponseWriter, r *http.Request) {
	var kartlar []odeme.YeniKart
	if !jsonOku(w, r, &kartlar) {
		return
	}
	eklenen, err := s.Odeme.TopluKartEkle(kartlar)
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, map[string]any{
		"basarili":     true,
		"eklenen_sayi": eklenen,
		"atlanan_sayi": len(kartlar) - eklenen,
	})
}

func (s *Sunucu) odemeHandler(w http.ResponseWriter, r *http.Request) {
	var istek odeme.OdemeIstegi
	if !jsonOku(w, r, &istek) {
		return
	}
	sonuc, err := s.Odeme.OdemeYap(kimlikAl(r), istek)
	if errors.Is(err, odeme.ErrYetersizBakiye) {
		// Reddedilen ödeme de kaydedilir; istemci ödeme kaydını ve bakiyeyi görebilsin.
		jsonYaz(w, http.StatusPaymentRequired, map[string]any{
			"basarili": false, "kod": odeme.ErrYetersizBakiye.Kod, "mesaj": err.Error(),
			"odeme": sonuc.Odeme, "kalan_bakiye": sonuc.KalanBakiye,
		})
		return
	}
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusCreated, map[string]any{
		"basarili": true, "odeme": sonuc.Odeme, "kalan_bakiye": sonuc.KalanBakiye,
	})
}

func (s *Sunucu) odemelerHandler(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			hataYaz(w, http.StatusBadRequest, "gecersiz_veri", "limit pozitif bir tam sayı olmalı")
			return
		}
		limit = n
	}
	sayfa, err := s.Odeme.Odemeler(kimlikAl(r), limit, r.URL.Query().Get("once"))
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, sayfa)
}

func (s *Sunucu) odemeGetirHandler(w http.ResponseWriter, r *http.Request) {
	o, err := s.Odeme.Odeme(kimlikAl(r), r.PathValue("id"))
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, o)
}

func (s *Sunucu) iadeHandler(w http.ResponseWriter, r *http.Request) {
	var istek struct {
		// Tutar boşsa iade edilebilir tutarın tamamı iade edilir.
		Tutar *para.Kurus `json:"tutar"`
	}
	if r.ContentLength != 0 && !jsonOku(w, r, &istek) {
		return
	}
	sonuc, err := s.Odeme.IadeEt(kimlikAl(r), r.PathValue("id"), istek.Tutar)
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, map[string]any{
		"basarili": true, "odeme": sonuc.Odeme, "kalan_bakiye": sonuc.KalanBakiye,
	})
}

type kullaniciGorunumu struct {
	ID        uint      `json:"id"`
	Eposta    string    `json:"eposta"`
	Rol       string    `json:"rol"`
	Olusturma time.Time `json:"olusturma"`
}

func (s *Sunucu) kullanicilarHandler(w http.ResponseWriter, r *http.Request) {
	liste, err := s.Kimlik.Kullanicilar()
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	sonuc := make([]kullaniciGorunumu, len(liste))
	for i, k := range liste {
		sonuc[i] = kullaniciGorunumu{k.ID, k.Eposta, k.Rol, k.CreatedAt}
	}
	jsonYaz(w, http.StatusOK, map[string]any{"kullanicilar": sonuc})
}

func (s *Sunucu) kullaniciOlusturHandler(w http.ResponseWriter, r *http.Request) {
	var istek struct {
		Eposta string `json:"eposta"`
		Sifre  string `json:"sifre"`
		Rol    string `json:"rol"`
	}
	if !jsonOku(w, r, &istek) {
		return
	}
	k, err := s.Kimlik.KullaniciOlustur(istek.Eposta, istek.Sifre, istek.Rol)
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusCreated, kullaniciGorunumu{k.ID, k.Eposta, k.Rol, k.CreatedAt})
}

func (s *Sunucu) mutabakatHandler(w http.ResponseWriter, r *http.Request) {
	rapor, err := s.Odeme.Mutabakat()
	if err != nil {
		isHatasiYaz(w, r, err)
		return
	}
	jsonYaz(w, http.StatusOK, rapor)
}

func (s *Sunucu) hazirHandler(w http.ResponseWriter, r *http.Request) {
	ctx, iptal := context.WithTimeout(r.Context(), 2*time.Second)
	defer iptal()
	sqlDB, err := s.DB.DB()
	if err == nil {
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		hataYaz(w, http.StatusServiceUnavailable, "hazir_degil", "Veritabanına ulaşılamıyor")
		return
	}
	jsonYaz(w, http.StatusOK, map[string]string{"durum": "hazir"})
}

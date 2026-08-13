package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go-ilk-proje/payment"
)

type LoginIstek struct {
	Eposta string `json:"eposta"`
	Sifre  string `json:"sifre"`
}

type OdemeIstek struct {
	KartNo string  `json:"kart_no"`
	Bakiye float64 `json:"bakiye"`
	Tutar  float64 `json:"tutar"`
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"basarili":false,"mesaj":"Sadece POST desteklenir"}`, http.StatusMethodNotAllowed)
		return
	}

	var istek LoginIstek
	err := json.NewDecoder(r.Body).Decode(&istek)
	if err != nil || istek.Eposta == "" || istek.Sifre == "" {
		http.Error(w, `{"basarili":false,"mesaj":"Geçersiz veri"}`, http.StatusBadRequest)
		return
	}

	if istek.Eposta == "enes@test.com" && istek.Sifre == "123456" {
		token, err := payment.TokenUret(istek.Eposta)
		if err != nil {
			http.Error(w, `{"basarili":false,"mesaj":"Token üretilemedi"}`, http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"basarili": true,
			"token":    token,
			"mesaj":    "Giriş başarılı",
		})
	} else {
		http.Error(w, `{"basarili":false,"mesaj":"Hatalı e-posta veya şifre"}`, http.StatusUnauthorized)
	}
}

func odemeHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"basarili":false,"mesaj":"Sadece POST desteklenir"}`, http.StatusMethodNotAllowed)
		return
	}

	var istek OdemeIstek
	err := json.NewDecoder(r.Body).Decode(&istek)
	if err != nil {
		http.Error(w, `{"basarili":false,"mesaj":"Geçersiz JSON formatı"}`, http.StatusBadRequest)
		return
	}

	kalanBakiye, err := payment.OdemeYap(istek.KartNo, istek.Bakiye, istek.Tutar)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"basarili":     false,
			"mesaj":        err.Error(),
			"kalan_bakiye": kalanBakiye,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"basarili":     true,
		"mesaj":        "Ödeme başarıyla tamamlandı",
		"kalan_bakiye": kalanBakiye,
	})
}

func islemlerHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var islemler []payment.IslemGecmisi
	payment.DB.Order("id desc").Limit(10).Find(&islemler)
	json.NewEncoder(w).Encode(islemler)
}

// 4. ÖZELLİK: TOPLU KART YÜKLEME ENDPOINT'İ
func topluKartHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"basarili":false,"mesaj":"Sadece POST"}`, http.StatusMethodNotAllowed)
		return
	}

	var kartlar []payment.KrediKarti
	err := json.NewDecoder(r.Body).Decode(&kartlar)
	if err != nil {
		http.Error(w, `{"basarili":false,"mesaj":"Geçersiz JSON formatı"}`, http.StatusBadRequest)
		return
	}

	eklenenSayi := 0
	for _, kart := range kartlar {
		if kart.KartNo != "" {
			var mevcudKart payment.KrediKarti
			res := payment.DB.Where("kart_no = ?", kart.KartNo).First(&mevcudKart)
			if res.Error != nil {
				payment.DB.Create(&kart)
				eklenenSayi++
			}
		}
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"basarili":     true,
		"eklenen_sayi": eklenenSayi,
		"mesaj":        fmt.Sprintf("%d yeni kart veritabanına eklendi!", eklenenSayi),
	})
}

func JWTMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"basarili":false,"mesaj":"Authorization başlığı eksik"}`, http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			http.Error(w, `{"basarili":false,"mesaj":"Geçersiz token formatı"}`, http.StatusUnauthorized)
			return
		}

		_, err := payment.TokenDogrula(parts[1])
		if err != nil {
			http.Error(w, `{"basarili":false,"mesaj":"Geçersiz veya süresi dolmuş token"}`, http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, "./templates/index.html")
}

func main() {
	_, err := payment.VeritabaniBaglan()
	if err != nil {
		panic(fmt.Sprintf("Veritabanına bağlanılamadı: %v", err))
	}

	
	http.HandleFunc("/api/v1/login", loginHandler)
	http.HandleFunc("/api/v1/pay", JWTMiddleware(odemeHandler))
	http.HandleFunc("/api/v1/transactions", JWTMiddleware(islemlerHandler))
	http.HandleFunc("/api/v1/cards/batch", JWTMiddleware(topluKartHandler))

	http.HandleFunc("/", indexHandler)

	fmt.Println("🚀 Korumalı Go Web Sunucusu 8080 portunda başlatılıyor...")
	err = http.ListenAndServe(":8080", nil)
	if err != nil {
		fmt.Printf("Sunucu başlatılamadı: %v\n", err)
	}
}

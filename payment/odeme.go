package payment

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

type KrediKarti struct {
	gorm.Model
	KartNo string  `gorm:"uniqueIndex" json:"kart_no"`
	Bakiye float64 `json:"bakiye"`
}

type IslemGecmisi struct {
	ID     uint      `gorm:"primaryKey" json:"id"`
	KartNo string    `json:"kart_no"`
	Tutar  float64   `json:"tutar"`
	Durum  string    `json:"durum"` // "Başarılı" veya "Yetersiz Bakiye" vb.
	Tarih  time.Time `json:"tarih"`
}

var jwtAnahtar = []byte("gizli_go_anahtari_123!")

func VeritabaniBaglan() (*gorm.DB, error) {
	db, err := gorm.Open(sqlite.Open("odeme_sistemi.db"), &gorm.Config{})
	if err != nil {
		return nil, err
	}

	// Yeni tabloyu da AutoMigrate ile ekliyoruz!
	err = db.AutoMigrate(&KrediKarti{}, &IslemGecmisi{})
	if err != nil {
		return nil, err
	}

	DB = db
	return db, nil
}

func OdemeYap(kartNo string, bakiye float64, tutar float64) (float64, error) {
	var kart KrediKarti
	result := DB.Where("kart_no = ?", kartNo).First(&kart)

	if result.Error != nil {
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			kart = KrediKarti{KartNo: kartNo, Bakiye: bakiye}
			DB.Create(&kart)
			fmt.Printf("🆕 Yeni kart kaydedildi: %s (Bakiye: %.2f)\n", kartNo, bakiye)
		} else {
			return 0, result.Error
		}
	}

	if kart.Bakiye < tutar {
		// Başarısız işlemi logla
		IslemKaydet(kartNo, tutar, "Yetersiz Bakiye")
		return kart.Bakiye, errors.New("yetersiz bakiye: işlem gerçekleştirilemedi")
	}

	kart.Bakiye -= tutar
	DB.Save(&kart)

	// Başarılı işlemi logla
	IslemKaydet(kartNo, tutar, "Başarılı")

	return kart.Bakiye, nil
}

func IslemKaydet(kartNo string, tutar float64, durum string) {
	islem := IslemGecmisi{
		KartNo: kartNo,
		Tutar:  tutar,
		Durum:  durum,
		Tarih:  time.Now(),
	}
	DB.Create(&islem)
}

func TokenUret(eposta string) (string, error) {
	claims := jwt.MapClaims{
		"eposta": eposta,
		"exp":    time.Now().Add(time.Hour * 24).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtAnahtar)
}

func TokenDogrula(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("beklenmeyen imza yöntemi")
		}
		return jwtAnahtar, nil
	})

	if err != nil || !token.Valid {
		return nil, errors.New("geçersiz veya süresi dolmuş token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("token verisi okunamadı")
	}

	return claims, nil
}

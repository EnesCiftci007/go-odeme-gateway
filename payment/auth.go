package payment

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	RolAdmin     = "admin"
	RolKullanici = "kullanici"

	tokenSuresi       = time.Hour
	minJWTAnahtarBoyu = 32
)

var (
	ErrHataliGiris     = errors.New("hatalı e-posta veya şifre")
	ErrGecersizToken   = errors.New("geçersiz veya süresi dolmuş token")
	ErrZayifJWTAnahtar = fmt.Errorf("JWT_SECRET en az %d karakter olmalı", minJWTAnahtarBoyu)
)

type Kullanici struct {
	gorm.Model
	Eposta    string `gorm:"uniqueIndex;not null"`
	SifreHash string `gorm:"not null"`
	Rol       string `gorm:"not null;default:kullanici"`
}

// Kimlik, doğrulanmış bir token'dan çıkan kullanıcı bilgisidir.
type Kimlik struct {
	Eposta string
	Rol    string
}

type tokenClaims struct {
	Eposta string `json:"eposta"`
	Rol    string `json:"rol"`
	jwt.RegisteredClaims
}

// Auth, JWT üretme/doğrulama ve kullanıcı girişini yönetir.
type Auth struct {
	db      *gorm.DB
	anahtar []byte
	// sahteHash, kullanıcı bulunamadığında da bcrypt karşılaştırması yapmak için kullanılır;
	// böylece yanıt süresinden e-postanın kayıtlı olup olmadığı anlaşılamaz.
	sahteHash []byte
}

func YeniAuth(db *gorm.DB, jwtAnahtar string) (*Auth, error) {
	if len(jwtAnahtar) < minJWTAnahtarBoyu {
		return nil, ErrZayifJWTAnahtar
	}
	sahte, err := bcrypt.GenerateFromPassword([]byte("sahte-sifre-zamanlama-icin"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	return &Auth{db: db, anahtar: []byte(jwtAnahtar), sahteHash: sahte}, nil
}

func epostaNormalize(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

// KullaniciOlustur, şifreyi bcrypt ile hash'leyerek yeni kullanıcı ekler.
func (a *Auth) KullaniciOlustur(eposta, sifre, rol string) error {
	eposta = epostaNormalize(eposta)
	if eposta == "" || !strings.Contains(eposta, "@") {
		return errors.New("geçersiz e-posta")
	}
	if len(sifre) < 8 {
		return errors.New("şifre en az 8 karakter olmalı")
	}
	if rol != RolAdmin && rol != RolKullanici {
		return errors.New("geçersiz rol")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(sifre), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return a.db.Create(&Kullanici{Eposta: eposta, SifreHash: string(hash), Rol: rol}).Error
}

// AdminYoksaOlustur, veritabanında hiç kullanıcı yoksa ilk admin hesabını açar.
func (a *Auth) AdminYoksaOlustur(eposta, sifre string) (bool, error) {
	var sayi int64
	if err := a.db.Model(&Kullanici{}).Count(&sayi).Error; err != nil {
		return false, err
	}
	if sayi > 0 {
		return false, nil
	}
	if eposta == "" || sifre == "" {
		return false, errors.New("veritabanında kullanıcı yok: ADMIN_EMAIL ve ADMIN_PASSWORD ortam değişkenlerini ayarlayın")
	}
	return true, a.KullaniciOlustur(eposta, sifre, RolAdmin)
}

// GirisYap, e-posta ve şifreyi doğrular ve başarılıysa bir token döndürür.
func (a *Auth) GirisYap(eposta, sifre string) (string, error) {
	var k Kullanici
	err := a.db.Where("eposta = ?", epostaNormalize(eposta)).First(&k).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	hash := a.sahteHash
	if err == nil {
		hash = []byte(k.SifreHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(sifre)) != nil || err != nil {
		return "", ErrHataliGiris
	}
	return a.TokenUret(k.Eposta, k.Rol)
}

func (a *Auth) TokenUret(eposta, rol string) (string, error) {
	simdi := time.Now()
	claims := tokenClaims{
		Eposta: eposta,
		Rol:    rol,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   eposta,
			IssuedAt:  jwt.NewNumericDate(simdi),
			ExpiresAt: jwt.NewNumericDate(simdi.Add(tokenSuresi)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.anahtar)
}

func (a *Auth) TokenDogrula(tokenStr string) (Kimlik, error) {
	var claims tokenClaims
	token, err := jwt.ParseWithClaims(tokenStr, &claims,
		func(*jwt.Token) (any, error) { return a.anahtar, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return Kimlik{}, ErrGecersizToken
	}
	return Kimlik{Eposta: claims.Eposta, Rol: claims.Rol}, nil
}

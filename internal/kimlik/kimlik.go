// Package kimlik, kullanıcı girişi, şifre saklama ve JWT işlemlerini yönetir.
package kimlik

import (
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
)

const (
	tokenSuresi       = time.Hour
	MinJWTAnahtarBoyu = 32
	minSifreBoyu      = 8
	maksSifreBoyu     = 72 // bcrypt 72 bayttan uzununu yok sayar
)

var (
	ErrHataliGiris      = errors.New("hatalı e-posta veya şifre")
	ErrGecersizToken    = errors.New("geçersiz veya süresi dolmuş token")
	ErrZayifJWTAnahtar  = fmt.Errorf("JWT_SECRET en az %d karakter olmalı", MinJWTAnahtarBoyu)
	ErrGecersizEposta   = errors.New("geçersiz e-posta adresi")
	ErrGecersizSifre    = fmt.Errorf("şifre %d-%d karakter olmalı", minSifreBoyu, maksSifreBoyu)
	ErrGecersizRol      = errors.New("rol 'admin' veya 'kullanici' olmalı")
	ErrEpostaKullanimda = errors.New("bu e-posta adresi zaten kayıtlı")
)

// Kimlik, doğrulanmış bir token'dan çıkan kullanıcı bilgisidir.
type Kimlik struct {
	ID     uint   `json:"id"`
	Eposta string `json:"eposta"`
	Rol    string `json:"rol"`
}

func (k Kimlik) Admin() bool { return k.Rol == veri.RolAdmin }

type tokenClaims struct {
	Eposta string `json:"eposta"`
	Rol    string `json:"rol"`
	jwt.RegisteredClaims
}

type Servis struct {
	db      *gorm.DB
	anahtar []byte
	// sahteHash, kullanıcı bulunamadığında da bcrypt karşılaştırması yapmak için kullanılır;
	// böylece yanıt süresinden e-postanın kayıtlı olup olmadığı anlaşılamaz.
	sahteHash []byte
	maliyet   int
}

// Yeni, kimlik servisini oluşturur. maliyet 0 ise bcrypt.DefaultCost kullanılır
// (testlerde bcrypt.MinCost ile hızlandırılabilir).
func Yeni(db *gorm.DB, jwtAnahtar string, maliyet int) (*Servis, error) {
	if len(jwtAnahtar) < MinJWTAnahtarBoyu {
		return nil, ErrZayifJWTAnahtar
	}
	if maliyet == 0 {
		maliyet = bcrypt.DefaultCost
	}
	sahte, err := bcrypt.GenerateFromPassword([]byte("sahte-sifre-zamanlama-icin"), maliyet)
	if err != nil {
		return nil, err
	}
	return &Servis{db: db, anahtar: []byte(jwtAnahtar), sahteHash: sahte, maliyet: maliyet}, nil
}

func EpostaNormalize(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

// KullaniciOlustur, şifreyi bcrypt ile hash'leyerek yeni kullanıcı ekler.
func (s *Servis) KullaniciOlustur(eposta, sifre, rol string) (*veri.Kullanici, error) {
	eposta = EpostaNormalize(eposta)
	if a, err := mail.ParseAddress(eposta); err != nil || a.Address != eposta || len(eposta) > 254 {
		return nil, ErrGecersizEposta
	}
	if len(sifre) < minSifreBoyu || len(sifre) > maksSifreBoyu {
		return nil, ErrGecersizSifre
	}
	if rol != veri.RolAdmin && rol != veri.RolKullanici {
		return nil, ErrGecersizRol
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(sifre), s.maliyet)
	if err != nil {
		return nil, err
	}
	k := &veri.Kullanici{Eposta: eposta, SifreHash: string(hash), Rol: rol}
	res := s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(k)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, ErrEpostaKullanimda
	}
	return k, nil
}

func (s *Servis) Kullanicilar() ([]veri.Kullanici, error) {
	var liste []veri.Kullanici
	err := s.db.Order("id").Find(&liste).Error
	return liste, err
}

// AdminYoksaOlustur, veritabanında hiç kullanıcı yoksa ilk admin hesabını açar.
func (s *Servis) AdminYoksaOlustur(eposta, sifre string) (bool, error) {
	var sayi int64
	if err := s.db.Model(&veri.Kullanici{}).Count(&sayi).Error; err != nil {
		return false, err
	}
	if sayi > 0 {
		return false, nil
	}
	if eposta == "" || sifre == "" {
		return false, errors.New("veritabanında kullanıcı yok: ADMIN_EMAIL ve ADMIN_PASSWORD ortam değişkenlerini ayarlayın")
	}
	_, err := s.KullaniciOlustur(eposta, sifre, veri.RolAdmin)
	return err == nil, err
}

// GirisYap, e-posta ve şifreyi doğrular ve başarılıysa bir token döndürür.
func (s *Servis) GirisYap(eposta, sifre string) (string, Kimlik, error) {
	var k veri.Kullanici
	err := s.db.Where("eposta = ?", EpostaNormalize(eposta)).First(&k).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", Kimlik{}, err
	}

	hash := s.sahteHash
	if err == nil {
		hash = []byte(k.SifreHash)
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(sifre)) != nil || err != nil {
		return "", Kimlik{}, ErrHataliGiris
	}
	kim := Kimlik{ID: k.ID, Eposta: k.Eposta, Rol: k.Rol}
	token, err := s.TokenUret(kim)
	return token, kim, err
}

func (s *Servis) TokenUret(k Kimlik) (string, error) {
	simdi := time.Now()
	claims := tokenClaims{
		Eposta: k.Eposta,
		Rol:    k.Rol,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatUint(uint64(k.ID), 10),
			IssuedAt:  jwt.NewNumericDate(simdi),
			ExpiresAt: jwt.NewNumericDate(simdi.Add(tokenSuresi)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.anahtar)
}

func (s *Servis) TokenDogrula(tokenStr string) (Kimlik, error) {
	var claims tokenClaims
	token, err := jwt.ParseWithClaims(tokenStr, &claims,
		func(*jwt.Token) (any, error) { return s.anahtar, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !token.Valid {
		return Kimlik{}, ErrGecersizToken
	}
	id, err := strconv.ParseUint(claims.Subject, 10, 32)
	if err != nil || id == 0 {
		return Kimlik{}, ErrGecersizToken
	}
	return Kimlik{ID: uint(id), Eposta: claims.Eposta, Rol: claims.Rol}, nil
}

package payment

import (
	"errors"
	"fmt"
	"log"
	"os"
	"regexp"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

type KrediKarti struct {
	gorm.Model
	KartNo string `gorm:"uniqueIndex;not null" json:"kart_no"`
	Bakiye Kurus  `gorm:"column:bakiye_kurus;not null;check:bakiye_kurus >= 0" json:"bakiye"`
}

type IslemGecmisi struct {
	ID     uint      `gorm:"primaryKey" json:"id"`
	KartNo string    `gorm:"index;not null" json:"kart_no"`
	Tutar  Kurus     `gorm:"column:tutar_kurus;not null" json:"tutar"`
	Durum  string    `gorm:"not null" json:"durum"`
	Tarih  time.Time `gorm:"not null" json:"tarih"`
}

const (
	DurumBasarili       = "Başarılı"
	DurumYetersizBakiye = "Yetersiz Bakiye"
	maksTopluKartSayisi = 1000
)

var (
	ErrKartBulunamadi = errors.New("kart bulunamadı")
	ErrYetersizBakiye = errors.New("yetersiz bakiye: işlem gerçekleştirilemedi")
	ErrGecersizKartNo = errors.New("geçersiz kart numarası")
	ErrCokFazlaKart   = fmt.Errorf("tek seferde en fazla %d kart yüklenebilir", maksTopluKartSayisi)
	kartNoDeseni      = regexp.MustCompile(`^[0-9]{4}([ -]?[0-9]{2,4}){1,4}$`)
)

// Servis, veritabanı bağlantısını taşır. Global değişken yerine bunu kullanmak
// testlerde her test için ayrı bir veritabanı açmayı mümkün kılar.
type Servis struct {
	DB *gorm.DB
}

// VeritabaniBaglan, SQLite veritabanını açar ve tabloları oluşturur.
func VeritabaniBaglan(yol string) (*Servis, error) {
	// busy_timeout: aynı anda gelen yazma isteklerinde "database is locked" yerine bekler.
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on", yol)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		// "kayıt bulunamadı" normal bir durumdur (örn. hatalı giriş denemesi);
		// loglanırsa denenen e-posta adresleri loglara sızar.
		Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		}),
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	// SQLite tek yazıcıyı destekler; tek bağlantı yarış durumlarını ve kilit hatalarını önler.
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&KrediKarti{}, &IslemGecmisi{}, &Kullanici{}); err != nil {
		return nil, err
	}
	return &Servis{DB: db}, nil
}

func KartNoGecerli(kartNo string) bool {
	return len(kartNo) <= 25 && kartNoDeseni.MatchString(kartNo)
}

func tutarGecerli(t Kurus) bool {
	return t > 0 && t <= MaxTutar
}

// OdemeYap, karttan tutarı düşer ve kalan bakiyeyi döndürür.
// Bakiye her zaman veritabanından okunur; istemci bakiye belirleyemez.
// Düşme işlemi tek bir koşullu UPDATE ile yapılır, böylece aynı anda gelen
// iki ödeme kartı eksi bakiyeye düşüremez.
func (s *Servis) OdemeYap(kartNo string, tutar Kurus) (Kurus, error) {
	if !KartNoGecerli(kartNo) {
		return 0, ErrGecersizKartNo
	}
	if !tutarGecerli(tutar) {
		return 0, ErrGecersizTutar
	}

	var kalan Kurus
	var sonucHata error

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&KrediKarti{}).
			Where("kart_no = ? AND bakiye_kurus >= ?", kartNo, tutar).
			Update("bakiye_kurus", gorm.Expr("bakiye_kurus - ?", tutar))
		if res.Error != nil {
			return res.Error
		}

		var kart KrediKarti
		if err := tx.Where("kart_no = ?", kartNo).First(&kart).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				sonucHata = ErrKartBulunamadi
				return nil
			}
			return err
		}
		kalan = kart.Bakiye

		durum := DurumBasarili
		if res.RowsAffected == 0 {
			durum = DurumYetersizBakiye
			sonucHata = ErrYetersizBakiye
		}
		return islemKaydet(tx, kartNo, tutar, durum)
	})
	if err != nil {
		return 0, err
	}
	return kalan, sonucHata
}

func islemKaydet(tx *gorm.DB, kartNo string, tutar Kurus, durum string) error {
	return tx.Create(&IslemGecmisi{
		KartNo: kartNo,
		Tutar:  tutar,
		Durum:  durum,
		Tarih:  time.Now().UTC(),
	}).Error
}

// SonIslemler, en son yapılan işlemleri döndürür.
func (s *Servis) SonIslemler(limit int) ([]IslemGecmisi, error) {
	islemler := []IslemGecmisi{}
	err := s.DB.Order("id desc").Limit(limit).Find(&islemler).Error
	return islemler, err
}

// TopluKartEkle, kartları tek transaction içinde ekler; zaten var olan kartları atlar.
// Kartlardan biri geçersizse hiçbiri eklenmez.
func (s *Servis) TopluKartEkle(kartlar []KrediKarti) (int64, error) {
	if len(kartlar) > maksTopluKartSayisi {
		return 0, ErrCokFazlaKart
	}
	for i, k := range kartlar {
		if !KartNoGecerli(k.KartNo) {
			return 0, fmt.Errorf("%d. kart: %w", i+1, ErrGecersizKartNo)
		}
		if k.Bakiye < 0 || k.Bakiye > MaxTutar {
			return 0, fmt.Errorf("%d. kart: %w", i+1, ErrGecersizTutar)
		}
	}
	if len(kartlar) == 0 {
		return 0, nil
	}

	var eklenen int64
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for _, k := range kartlar {
			kart := KrediKarti{KartNo: k.KartNo, Bakiye: k.Bakiye}
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&kart)
			if res.Error != nil {
				return res.Error
			}
			eklenen += res.RowsAffected
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return eklenen, nil
}

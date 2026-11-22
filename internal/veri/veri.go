// Package veri, veritabanı modellerini ve bağlantıyı içerir.
package veri

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/para"
)

const (
	RolAdmin     = "admin"
	RolKullanici = "kullanici"
)

// Ödeme durumları.
const (
	DurumBasarili   = "basarili"
	DurumReddedildi = "reddedildi"
	DurumKismiIade  = "kismi_iade"
	DurumIadeEdildi = "iade_edildi"
)

// Defter hesapları. Her para hareketi iki kayıttan oluşur ve toplamı sıfırdır.
const (
	HesapIsyeri = "isyeri" // ödemelerin aktarıldığı işyeri hesabı
	HesapSistem = "sistem" // kartlara yüklenen bakiyenin karşı hesabı
)

func KartHesabi(kartID uint) string { return "kart:" + strconv.FormatUint(uint64(kartID), 10) }

// Hareket türleri.
const (
	TurYukleme = "yukleme"
	TurOdeme   = "odeme"
	TurIade    = "iade"
)

type Kullanici struct {
	ID        uint   `gorm:"primaryKey"`
	Eposta    string `gorm:"uniqueIndex;not null;size:254"`
	SifreHash string `gorm:"not null"`
	Rol       string `gorm:"not null;size:20"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Kullanici) TableName() string { return "kullanicilar" }

type Kart struct {
	ID        uint       `gorm:"primaryKey"`
	KartNo    string     `gorm:"uniqueIndex;not null;size:25"`
	SahipID   *uint      `gorm:"index"`
	Sahip     *Kullanici `gorm:"foreignKey:SahipID"`
	Bakiye    para.Kurus `gorm:"column:bakiye_kurus;not null;check:chk_kart_bakiye,bakiye_kurus >= 0"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (Kart) TableName() string { return "kartlar" }

type Odeme struct {
	ID          uint       `gorm:"primaryKey"`
	RefNo       string     `gorm:"uniqueIndex;not null;size:40"`
	KartID      uint       `gorm:"index;not null"`
	Kart        Kart       `gorm:"foreignKey:KartID"`
	KullaniciID uint       `gorm:"index;not null"`
	Tutar       para.Kurus `gorm:"column:tutar_kurus;not null"`
	IadeEdilen  para.Kurus `gorm:"column:iade_edilen_kurus;not null;default:0"`
	Durum       string     `gorm:"index;not null;size:20"`
	Aciklama    string     `gorm:"size:140"`
	CreatedAt   time.Time  `gorm:"index"`
	UpdatedAt   time.Time
}

func (Odeme) TableName() string { return "odemeler" }

// Hareket, çift taraflı defterin bir satırıdır. Tutar işaretlidir:
// hesaba giren para pozitif, çıkan para negatiftir.
type Hareket struct {
	ID        uint       `gorm:"primaryKey"`
	OdemeID   *uint      `gorm:"index"`
	Hesap     string     `gorm:"index;not null;size:40"`
	Tutar     para.Kurus `gorm:"column:tutar_kurus;not null"`
	Tur       string     `gorm:"not null;size:20"`
	CreatedAt time.Time
}

func (Hareket) TableName() string { return "hareketler" }

// IdempotencyKaydi, aynı Idempotency-Key ile tekrar gelen isteğe ilk yanıtın aynısını döndürmek için tutulur.
// YanitKod 0 ise istek hâlâ işleniyordur.
type IdempotencyKaydi struct {
	ID          uint   `gorm:"primaryKey"`
	KullaniciID uint   `gorm:"not null;uniqueIndex:idx_idem_anahtar"`
	Anahtar     string `gorm:"not null;size:255;uniqueIndex:idx_idem_anahtar"`
	IstekOzeti  string `gorm:"not null;size:64"`
	YanitKod    int    `gorm:"not null;default:0"`
	YanitGovde  []byte
	CreatedAt   time.Time `gorm:"index"`
}

func (IdempotencyKaydi) TableName() string { return "idempotency_kayitlari" }

// Webhook olay durumları.
const (
	OlayBekliyor   = "bekliyor"
	OlayGonderildi = "gonderildi"
	OlayBasarisiz  = "basarisiz"
)

// WebhookOlayi, "outbox" deseniyle ödeme ile aynı transaction içinde yazılır;
// böylece ödeme kaydedilip olay kaybolamaz. Arka plandaki gönderici bunları teslim eder.
type WebhookOlayi struct {
	ID            uint      `gorm:"primaryKey"`
	OlayID        string    `gorm:"uniqueIndex;not null;size:40"`
	Tur           string    `gorm:"not null;size:40"`
	Govde         []byte    `gorm:"not null"`
	Durum         string    `gorm:"index:idx_olay_kuyruk;not null;size:20"`
	Deneme        int       `gorm:"not null;default:0"`
	SonrakiDeneme time.Time `gorm:"index:idx_olay_kuyruk;not null"`
	SonHata       string    `gorm:"size:500"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (WebhookOlayi) TableName() string { return "webhook_olaylari" }

func Modeller() []any {
	return []any{&Kullanici{}, &Kart{}, &Odeme{}, &Hareket{}, &IdempotencyKaydi{}, &WebhookOlayi{}}
}

// Ac, veritabanına bağlanır ve tabloları oluşturur/günceller.
// surucu "sqlite" veya "postgres" olabilir.
func Ac(surucu, dsn string) (*gorm.DB, error) {
	db, err := AcHam(surucu, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(Modeller()...); err != nil {
		Kapat(db)
		return nil, fmt.Errorf("tablolar oluşturulamadı: %w", err)
	}
	return db, nil
}

// AcHam, tabloları değiştirmeden yalnızca bağlantıyı açar.
func AcHam(surucu, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{
		// "kayıt bulunamadı" normal bir durumdur (örn. hatalı giriş denemesi);
		// loglanırsa denenen e-posta adresleri loglara sızar.
		Logger: logger.New(log.New(os.Stderr, "", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		}),
		NowFunc: func() time.Time { return time.Now().UTC() },
	}

	var db *gorm.DB
	var err error
	switch surucu {
	case "sqlite":
		// busy_timeout: aynı anda gelen yazma isteklerinde "database is locked" yerine bekler.
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		db, err = gorm.Open(sqlite.Open("file:"+dsn+sep+"_busy_timeout=5000&_journal_mode=WAL&_foreign_keys=on&_txlock=immediate"), cfg)
		if err == nil {
			sqlDB, e := db.DB()
			if e != nil {
				return nil, e
			}
			// SQLite tek yazıcıyı destekler; tek bağlantı kilit hatalarını önler.
			sqlDB.SetMaxOpenConns(1)
		}
	case "postgres":
		db, err = gorm.Open(postgres.Open(dsn), cfg)
		if err == nil {
			sqlDB, e := db.DB()
			if e != nil {
				return nil, e
			}
			sqlDB.SetMaxOpenConns(20)
			sqlDB.SetMaxIdleConns(5)
			sqlDB.SetConnMaxLifetime(30 * time.Minute)
		}
	default:
		return nil, fmt.Errorf("bilinmeyen veritabanı sürücüsü %q (sqlite veya postgres olmalı)", surucu)
	}
	if err != nil {
		return nil, err
	}
	return db, nil
}

func Kapat(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

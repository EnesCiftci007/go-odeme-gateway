// Package veritest, testler için izole bir veritabanı açar.
//
// TEST_DATABASE_URL ayarlıysa testler PostgreSQL'de, her test için ayrı bir şemada çalışır;
// ayarlı değilse geçici bir SQLite dosyası kullanılır.
package veritest

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
)

func Ac(t testing.TB) *gorm.DB {
	t.Helper()

	pgURL := os.Getenv("TEST_DATABASE_URL")
	if pgURL == "" {
		db, err := veri.Ac("sqlite", filepath.Join(t.TempDir(), "test.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { veri.Kapat(db) })
		return db
	}

	b := make([]byte, 6)
	rand.Read(b)
	sema := "test_" + hex.EncodeToString(b)

	yonetici, err := veri.AcHam("postgres", pgURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := yonetici.Exec("CREATE SCHEMA " + sema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		yonetici.Exec("DROP SCHEMA " + sema + " CASCADE")
		veri.Kapat(yonetici)
	})

	u, err := url.Parse(pgURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", sema)
	u.RawQuery = q.Encode()

	db, err := veri.Ac("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { veri.Kapat(db) })
	return db
}

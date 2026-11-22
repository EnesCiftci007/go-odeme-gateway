package para

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Kurus, para tutarlarını kuruş cinsinden tam sayı olarak tutar.
// float64 kullanılmaz; böylece 0.1 + 0.2 gibi yuvarlama hataları oluşmaz.
// JSON'da TL cinsinden ondalık sayı olarak görünür: 250.5 -> 25050 kuruş.
type Kurus int64

// MaxTutar, tek bir işlemde veya bakiyede izin verilen en yüksek tutardır (10 milyon TL).
const MaxTutar Kurus = 10_000_000 * 100

var ErrGecersizTutar = errors.New("geçersiz tutar")

// KurusParse, "250", "250.5" veya "250.50" gibi bir metni float'a çevirmeden kuruşa çevirir.
// En fazla iki ondalık basamağa izin verir.
func KurusParse(s string) (Kurus, error) {
	s = strings.TrimSpace(s)
	negatif := strings.HasPrefix(s, "-")
	if negatif || strings.HasPrefix(s, "+") {
		s = s[1:]
	}

	tam, kesir, noktaVar := strings.Cut(s, ".")
	if tam == "" || (noktaVar && kesir == "") || len(kesir) > 2 || len(tam) > 12 {
		return 0, ErrGecersizTutar
	}
	for _, c := range tam + kesir {
		if c < '0' || c > '9' {
			return 0, ErrGecersizTutar
		}
	}
	for len(kesir) < 2 {
		kesir += "0"
	}

	tl, err := strconv.ParseInt(tam, 10, 64)
	if err != nil {
		return 0, ErrGecersizTutar
	}
	kr, _ := strconv.ParseInt(kesir, 10, 64)

	k := Kurus(tl*100 + kr)
	if negatif {
		k = -k
	}
	return k, nil
}

func (k Kurus) String() string {
	isaret := ""
	v := int64(k)
	if v < 0 {
		isaret = "-"
		v = -v
	}
	return fmt.Sprintf("%s%d.%02d", isaret, v/100, v%100)
}

func (k Kurus) MarshalJSON() ([]byte, error) {
	return []byte(k.String()), nil
}

func (k *Kurus) UnmarshalJSON(b []byte) error {
	// Hem 250.5 hem de "250.5" kabul edilir.
	b = bytes.Trim(b, `"`)
	v, err := KurusParse(string(b))
	if err != nil {
		return err
	}
	*k = v
	return nil
}

// Value ve Scan, tipin her veritabanı sürücüsünde (SQLite, PostgreSQL) BIGINT olarak saklanmasını sağlar.
func (k Kurus) Value() (driver.Value, error) {
	return int64(k), nil
}

func (k *Kurus) Scan(src any) error {
	switch v := src.(type) {
	case int64:
		*k = Kurus(v)
	case int32:
		*k = Kurus(v)
	case nil:
		*k = 0
	default:
		return fmt.Errorf("Kurus: desteklenmeyen tip %T", src)
	}
	return nil
}

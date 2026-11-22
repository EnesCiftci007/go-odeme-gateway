package payment

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func testServis(t *testing.T) *Servis {
	t.Helper()
	s, err := VeritabaniBaglan(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db, err := s.DB.DB(); err == nil {
			db.Close()
		}
	})
	return s
}

func kartEkle(t *testing.T, s *Servis, kartNo string, bakiye Kurus) {
	t.Helper()
	if _, err := s.TopluKartEkle([]KrediKarti{{KartNo: kartNo, Bakiye: bakiye}}); err != nil {
		t.Fatal(err)
	}
}

func TestKurusParse(t *testing.T) {
	gecerli := map[string]Kurus{
		"0": 0, "250": 25000, "250.5": 25050, "250.50": 25050, "0.01": 1, "-3.2": -320, "+7": 700,
	}
	for girdi, beklenen := range gecerli {
		got, err := KurusParse(girdi)
		if err != nil || got != beklenen {
			t.Errorf("KurusParse(%q) = %v, %v; beklenen %v", girdi, got, err, beklenen)
		}
	}
	for _, girdi := range []string{"", "abc", "1.234", "1.", ".5", "1e5", "1,5", "--1", "NaN", "9999999999999"} {
		if _, err := KurusParse(girdi); err == nil {
			t.Errorf("KurusParse(%q) hata vermeliydi", girdi)
		}
	}
}

func TestKurusJSON(t *testing.T) {
	var v struct{ T Kurus }
	if err := json.Unmarshal([]byte(`{"T":0.1}`), &v); err != nil || v.T != 10 {
		t.Fatalf("0.1 -> %v, %v", v.T, err)
	}
	if err := json.Unmarshal([]byte(`{"T":"19.99"}`), &v); err != nil || v.T != 1999 {
		t.Fatalf(`"19.99" -> %v, %v`, v.T, err)
	}
	b, _ := json.Marshal(struct{ T Kurus }{1999})
	if string(b) != `{"T":19.99}` {
		t.Fatalf("marshal: %s", b)
	}
}

func TestOdemeBasarili(t *testing.T) {
	s := testServis(t)
	kartEkle(t, s, "1111-2222", 100000)

	kalan, err := s.OdemeYap("1111-2222", 25050)
	if err != nil || kalan != 74950 {
		t.Fatalf("kalan=%v err=%v", kalan, err)
	}
	islemler, _ := s.SonIslemler(10)
	if len(islemler) != 1 || islemler[0].Durum != DurumBasarili {
		t.Fatalf("işlem kaydı yanlış: %+v", islemler)
	}
}

func TestOdemeYetersizBakiye(t *testing.T) {
	s := testServis(t)
	kartEkle(t, s, "1111-2222", 1000)

	kalan, err := s.OdemeYap("1111-2222", 1001)
	if !errors.Is(err, ErrYetersizBakiye) || kalan != 1000 {
		t.Fatalf("kalan=%v err=%v", kalan, err)
	}
	islemler, _ := s.SonIslemler(10)
	if len(islemler) != 1 || islemler[0].Durum != DurumYetersizBakiye {
		t.Fatalf("başarısız işlem kaydedilmeli: %+v", islemler)
	}
}

func TestOdemeBilinmeyenKartOlusturmaz(t *testing.T) {
	s := testServis(t)
	if _, err := s.OdemeYap("5555-6666", 100); !errors.Is(err, ErrKartBulunamadi) {
		t.Fatalf("err=%v", err)
	}
	var sayi int64
	s.DB.Model(&KrediKarti{}).Count(&sayi)
	if sayi != 0 {
		t.Fatal("ödeme isteği yeni kart oluşturmamalı")
	}
}

func TestOdemeGecersizTutar(t *testing.T) {
	s := testServis(t)
	kartEkle(t, s, "1111-2222", 1000)
	for _, tutar := range []Kurus{0, -500, MaxTutar + 1} {
		if _, err := s.OdemeYap("1111-2222", tutar); !errors.Is(err, ErrGecersizTutar) {
			t.Errorf("tutar %v: err=%v", tutar, err)
		}
	}
	var kart KrediKarti
	s.DB.Where("kart_no = ?", "1111-2222").First(&kart)
	if kart.Bakiye != 1000 {
		t.Fatalf("bakiye değişmemeliydi: %v", kart.Bakiye)
	}
}

// 100 eşzamanlı ödeme; bakiye yalnızca 10 tanesine yeter.
// Tam olarak 10 başarılı olmalı ve bakiye 0'ın altına düşmemeli.
func TestOdemeEszamanli(t *testing.T) {
	s := testServis(t)
	kartEkle(t, s, "1111-2222", 10000)

	var wg sync.WaitGroup
	var mu sync.Mutex
	basarili := 0
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.OdemeYap("1111-2222", 1000)
			if err == nil {
				mu.Lock()
				basarili++
				mu.Unlock()
			} else if !errors.Is(err, ErrYetersizBakiye) {
				t.Errorf("beklenmeyen hata: %v", err)
			}
		}()
	}
	wg.Wait()

	var kart KrediKarti
	s.DB.Where("kart_no = ?", "1111-2222").First(&kart)
	if basarili != 10 || kart.Bakiye != 0 {
		t.Fatalf("basarili=%d bakiye=%v (beklenen 10 ve 0)", basarili, kart.Bakiye)
	}
}

func TestTopluKartEkle(t *testing.T) {
	s := testServis(t)
	n, err := s.TopluKartEkle([]KrediKarti{{KartNo: "1111-2222", Bakiye: 100}, {KartNo: "3333-4444", Bakiye: 200}})
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Var olan kart atlanır, bakiyesi ezilmez.
	n, err = s.TopluKartEkle([]KrediKarti{{KartNo: "1111-2222", Bakiye: 999999}})
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	// Geçersiz kart varsa hiçbiri eklenmez.
	_, err = s.TopluKartEkle([]KrediKarti{{KartNo: "7777-8888", Bakiye: 1}, {KartNo: `<img src=x onerror=alert(1)>`}})
	if !errors.Is(err, ErrGecersizKartNo) {
		t.Fatalf("err=%v", err)
	}
	_, err = s.TopluKartEkle([]KrediKarti{{KartNo: "7777-8888", Bakiye: -1}})
	if !errors.Is(err, ErrGecersizTutar) {
		t.Fatalf("negatif bakiye: err=%v", err)
	}
	var sayi int64
	s.DB.Model(&KrediKarti{}).Count(&sayi)
	if sayi != 2 {
		t.Fatalf("kart sayısı %d, beklenen 2", sayi)
	}
}

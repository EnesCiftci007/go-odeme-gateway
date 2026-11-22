package para

import (
	"encoding/json"
	"testing"
)

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
	b, _ = json.Marshal(struct{ T Kurus }{-5})
	if string(b) != `{"T":-0.05}` {
		t.Fatalf("negatif marshal: %s", b)
	}
}

func TestKurusScan(t *testing.T) {
	var k Kurus
	if err := k.Scan(int64(1234)); err != nil || k != 1234 {
		t.Fatalf("%v %v", k, err)
	}
	if err := k.Scan("x"); err == nil {
		t.Fatal("string kabul edilmemeli")
	}
	if v, _ := Kurus(99).Value(); v != int64(99) {
		t.Fatalf("Value: %v", v)
	}
}

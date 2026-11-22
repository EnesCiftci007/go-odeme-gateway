package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
)

type baglamAnahtari int

const (
	kimlikAnahtari baglamAnahtari = iota
	istekIDAnahtari
)

// durumYazici, yazılan HTTP durum kodunu yakalar (loglama ve metrikler için).
type durumYazici struct {
	http.ResponseWriter
	durum int
	boyut int
}

func (d *durumYazici) WriteHeader(kod int) {
	if d.durum == 0 {
		d.durum = kod
	}
	d.ResponseWriter.WriteHeader(kod)
}

func (d *durumYazici) Write(b []byte) (int, error) {
	if d.durum == 0 {
		d.durum = http.StatusOK
	}
	n, err := d.ResponseWriter.Write(b)
	d.boyut += n
	return n, err
}

func (d *durumYazici) Unwrap() http.ResponseWriter { return d.ResponseWriter }

func sar(w http.ResponseWriter) *durumYazici {
	if d, ok := w.(*durumYazici); ok {
		return d
	}
	return &durumYazici{ResponseWriter: w}
}

// istekKimligi, her isteğe bir X-Request-ID atar (gelen istekte varsa onu kullanır).
func istekKimligi(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), istekIDAnahtari, id)))
	})
}

func istekKimligiAl(ctx context.Context) string {
	id, _ := ctx.Value(istekIDAnahtari).(string)
	return id
}

// kurtar, handler'daki bir panic'in sunucuyu düşürmesini engeller.
func kurtar(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				slog.Error("panic", "deger", v, "istek_id", istekKimligiAl(r.Context()), "yigin", string(debug.Stack()))
				hataYaz(w, http.StatusInternalServerError, "sunucu_hatasi", "Sunucu hatası")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Sunucu) logla(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bas := time.Now()
		dw := sar(w)
		next.ServeHTTP(dw, r)
		seviye := slog.LevelInfo
		if dw.durum >= 500 {
			seviye = slog.LevelError
		}
		slog.Log(r.Context(), seviye, "istek",
			"metot", r.Method, "yol", r.URL.Path, "durum", dw.durum, "boyut", dw.boyut,
			"sure_ms", time.Since(bas).Milliseconds(), "ip", s.istemciIP(r), "istek_id", istekKimligiAl(r.Context()))
	})
}

// guvenlikBasliklari, tarayıcıya sıkı bir içerik politikası (CSP) ve diğer güvenlik başlıklarını gönderir.
// CSP yalnızca kendi alan adımızdan script ve stil yüklenmesine izin verir; satır içi script çalışmaz.
func guvenlikBasliklari(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
			"connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// ---- Kimlik doğrulama ----

func kimlikAl(r *http.Request) kimlik.Kimlik {
	k, _ := r.Context().Value(kimlikAnahtari).(kimlik.Kimlik)
	return k
}

func (s *Sunucu) oturumGerekli(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tokenStr, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tokenStr == "" {
			hataYaz(w, http.StatusUnauthorized, "oturum_gerekli", "Authorization başlığı eksik veya hatalı")
			return
		}
		k, err := s.Kimlik.TokenDogrula(tokenStr)
		if err != nil {
			hataYaz(w, http.StatusUnauthorized, "gecersiz_token", "Geçersiz veya süresi dolmuş token")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), kimlikAnahtari, k)))
	}
}

func (s *Sunucu) adminGerekli(next http.HandlerFunc) http.HandlerFunc {
	return s.oturumGerekli(func(w http.ResponseWriter, r *http.Request) {
		if !kimlikAl(r).Admin() {
			hataYaz(w, http.StatusForbidden, "yetki_yok", "Bu işlem için admin yetkisi gerekli")
			return
		}
		next(w, r)
	})
}

// ---- İstek sınırlama ----

type ziyaretci struct {
	lim *rate.Limiter
	son time.Time
}

// limitci, IP başına "token bucket" ile istek sınırlar.
type limitci struct {
	mu       sync.Mutex
	hiz      rate.Limit
	kova     int
	z        map[string]*ziyaretci
	temizlik time.Time
}

func yeniLimitci(hiz rate.Limit, kova int) *limitci {
	return &limitci{hiz: hiz, kova: kova, z: map[string]*ziyaretci{}, temizlik: time.Now()}
}

// izin, isteğe izin verilip verilmediğini ve verilmediyse kaç saniye beklenmesi gerektiğini döndürür.
func (l *limitci) izin(ip string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	simdi := time.Now()
	// Uzun süredir görülmeyen IP'leri dakikada bir temizle; bellek sınırsız büyümesin.
	if simdi.Sub(l.temizlik) > time.Minute {
		for k, v := range l.z {
			if simdi.Sub(v.son) > 10*time.Minute {
				delete(l.z, k)
			}
		}
		l.temizlik = simdi
	}
	v, ok := l.z[ip]
	if !ok {
		v = &ziyaretci{lim: rate.NewLimiter(l.hiz, l.kova)}
		l.z[ip] = v
	}
	v.son = simdi
	rez := v.lim.ReserveN(simdi, 1)
	if gecikme := rez.DelayFrom(simdi); gecikme > 0 {
		rez.CancelAt(simdi)
		return false, int(math.Ceil(gecikme.Seconds()))
	}
	return true, 0
}

func (s *Sunucu) limitle(l *limitci, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ok, bekle := l.izin(s.istemciIP(r)); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(bekle))
			hataYaz(w, http.StatusTooManyRequests, "cok_fazla_istek", "Çok fazla istek; lütfen biraz bekleyin")
			return
		}
		next(w, r)
	})
}

func (s *Sunucu) istemciIP(r *http.Request) string {
	if s.ProxyArkasinda {
		// Güvenilir proxy'nin eklediği son adres gerçek istemcidir.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parcalar := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parcalar[len(parcalar)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrikler, Prometheus formatında /metrics adresinden sunulur.
type Metrikler struct {
	kayit         *prometheus.Registry
	istekler      *prometheus.CounterVec
	istekSuresi   *prometheus.HistogramVec
	odemeler      *prometheus.CounterVec
	webhookTeslim *prometheus.CounterVec
}

func YeniMetrikler() *Metrikler {
	m := &Metrikler{
		kayit: prometheus.NewRegistry(),
		istekler: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_istekleri_toplam", Help: "Rota, metot ve durum koduna göre HTTP istekleri.",
		}, []string{"rota", "durum"}),
		istekSuresi: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_istek_suresi_saniye", Help: "HTTP isteklerinin işlenme süresi.",
			Buckets: prometheus.DefBuckets,
		}, []string{"rota"}),
		odemeler: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "odeme_islemleri_toplam", Help: "Sonuca göre ödeme ve iade işlemleri.",
		}, []string{"sonuc"}),
		webhookTeslim: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "webhook_teslimleri_toplam", Help: "Webhook teslim denemeleri.",
		}, []string{"sonuc"}),
	}
	m.kayit.MustRegister(m.istekler, m.istekSuresi, m.odemeler, m.webhookTeslim,
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrikler) Handler() http.Handler {
	return promhttp.HandlerFor(m.kayit, promhttp.HandlerOpts{})
}

func (m *Metrikler) OdemeSonucu(sonuc string) { m.odemeler.WithLabelValues(sonuc).Inc() }

func (m *Metrikler) WebhookSonucu(basarili bool) {
	if basarili {
		m.webhookTeslim.WithLabelValues("basarili").Inc()
	} else {
		m.webhookTeslim.WithLabelValues("basarisiz").Inc()
	}
}

// ara, her isteği rota desenine göre sayar. Ham yol yerine desen ("/api/v1/payments/{id}")
// kullanılır; aksi halde her ödeme ID'si ayrı bir metrik serisi oluştururdu.
func (m *Metrikler) ara(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bas := time.Now()
		dw := sar(w)
		next.ServeHTTP(dw, r)
		rota := r.Pattern
		if rota == "" {
			rota = "eslesmedi"
		}
		m.istekler.WithLabelValues(rota, strconv.Itoa(dw.durum)).Inc()
		m.istekSuresi.WithLabelValues(rota).Observe(time.Since(bas).Seconds())
	})
}

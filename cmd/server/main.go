// Komut server, ödeme gateway HTTP sunucusunu başlatır.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/EnesCiftci007/go-odeme-gateway/internal/api"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/ayarlar"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/kimlik"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/odeme"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/veri"
	"github.com/EnesCiftci007/go-odeme-gateway/internal/webhook"
	"github.com/EnesCiftci007/go-odeme-gateway/web"
)

func main() {
	if err := calistir(); err != nil {
		slog.Error("sunucu durdu", "hata", err)
		os.Exit(1)
	}
}

func calistir() error {
	a, err := ayarlar.Oku(os.Getenv)
	if err != nil {
		return fmt.Errorf("ayarlar geçersiz:\n%w", err)
	}
	if a.LogFormati == "json" {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	}

	db, err := veri.Ac(a.DBSurucu, a.DBAdres)
	if err != nil {
		return fmt.Errorf("veritabanına bağlanılamadı: %w", err)
	}
	defer veri.Kapat(db)

	ks, err := kimlik.Yeni(db, a.JWTAnahtar, 0)
	if err != nil {
		return err
	}
	olustu, err := ks.AdminYoksaOlustur(a.AdminEposta, a.AdminSifre)
	if err != nil {
		return fmt.Errorf("admin hesabı oluşturulamadı: %w", err)
	}
	if olustu {
		slog.Info("ilk admin hesabı oluşturuldu", "eposta", a.AdminEposta)
	}

	metrik := api.YeniMetrikler()
	odemeServis := odeme.Yeni(db, a.WebhookURL != "")
	odemeServis.OdemeSonucu = metrik.OdemeSonucu

	statik, err := web.Statik()
	if err != nil {
		return err
	}
	sunucu := &api.Sunucu{
		DB: db, Kimlik: ks, Odeme: odemeServis, Metrik: metrik,
		Statik: statik, OpenAPI: web.OpenAPI, ProxyArkasinda: a.ProxyArkasinda,
	}
	srv := &http.Server{
		Addr:              ":" + a.Port,
		Handler:           sunucu.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, durdur := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer durdur()

	var arkaPlan sync.WaitGroup
	arkaPlan.Go(func() { api.IdempotencyTemizle(ctx, db) })
	if a.WebhookURL != "" {
		g := &webhook.Gonderici{DB: db, URL: a.WebhookURL, Gizli: []byte(a.WebhookGizli), Sonuc: metrik.WebhookSonucu}
		arkaPlan.Go(func() { g.Calistir(ctx) })
		slog.Info("webhook göndericisi açık", "url", a.WebhookURL)
	}

	hata := make(chan error, 1)
	go func() {
		slog.Info("sunucu başlatılıyor", "adres", srv.Addr, "veritabani", a.DBSurucu)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			hata <- err
		}
		close(hata)
	}()

	select {
	case err := <-hata:
		return err
	case <-ctx.Done():
	}

	// Kapanış: yeni bağlantı alma, açık isteklerin bitmesini en fazla 10 sn bekle.
	slog.Info("kapatılıyor...")
	kapatCtx, iptal := context.WithTimeout(context.Background(), 10*time.Second)
	defer iptal()
	err = srv.Shutdown(kapatCtx)
	arkaPlan.Wait()
	return err
}

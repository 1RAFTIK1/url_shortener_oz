// Команда shortener — HTTP-сервис сокращения ссылок.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/1RAFTIK1/url-shortener-oz/internal/codegen"
	"github.com/1RAFTIK1/url-shortener-oz/internal/config"
	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
	"github.com/1RAFTIK1/url-shortener-oz/internal/storage"
	"github.com/1RAFTIK1/url-shortener-oz/internal/transport/httpapi"
)

// main держится тонким вся логика в run, который возвращает ошибку
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "shortener:", err)
		os.Exit(1)
	}
}

// run — композиционный корень
func run() error {
	cfg, err := config.Load(os.Args[1:], os.LookupEnv, os.Stderr)
	if err != nil {
		return err
	}

	// Режим проверки живости для HEALTHCHECK в образе
	if cfg.Healthcheck {
		return healthcheck(cfg)
	}

	// JSON-логи
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	// Контекст, отменяемый по SIGINT/SIGTERM
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	store, err := storage.New(ctx, cfg, log)
	if err != nil {
		return fmt.Errorf("инициализация хранилища: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			log.Error("не удалось закрыть хранилище", "error", err)
		}
	}()

	svc := shortener.NewService(store, codegen.New(), log)

	router := httpapi.NewRouter(svc, store, log, httpapi.RouterConfig{
		BaseURL:        cfg.BaseURL,
		RequestTimeout: cfg.RequestTimeout,
	})

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: router,
		// ReadHeaderTimeout закрывает Slowloris
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// Буферизованный канал
	errCh := make(chan error, 1)
	go func() {
		log.Info("сервис запущен",
			"addr", cfg.HTTPAddr, "storage", cfg.Storage, "base_url", cfg.BaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP-сервер: %w", err)
	case <-ctx.Done():
		log.Info("получен сигнал остановки, завершаем активные запросы",
			"timeout", cfg.ShutdownTimeout)
	}

	// Новый контекст, а не ctx
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("остановка HTTP-сервера: %w", err)
	}

	log.Info("сервис остановлен")
	return nil
}

// healthcheck ходит на собственный /healthz и возвращает ошибку, если сервис
// не отвечает
func healthcheck(cfg config.Config) error {
	host, port, err := net.SplitHostPort(cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("разбор -http-addr: %w", err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	client := &http.Client{Timeout: 2 * time.Second}
	url := "http://" + net.JoinHostPort(host, port) + "/healthz"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz вернул статус %d", resp.StatusCode)
	}
	return nil
}

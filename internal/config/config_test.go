package config

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func noEnv(string) (string, bool) { return "", false }

func envMap(m map[string]string) LookupEnv {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(nil, noEnv, io.Discard)
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
	if cfg.Storage != StorageMemory {
		t.Errorf("Storage = %q, ожидалось %q", cfg.Storage, StorageMemory)
	}
	if cfg.HTTPAddr != ":8081" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.BaseURL != "http://localhost:8081" {
		t.Errorf("BaseURL = %q, ожидался выведенный из -http-addr", cfg.BaseURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
}

// Приоритет источников
func TestLoadFlagBeatsEnv(t *testing.T) {
	env := envMap(map[string]string{"HTTP_ADDR": ":9999"})

	cfg, err := Load([]string{"-http-addr", ":7777"}, env, io.Discard)
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
	if cfg.HTTPAddr != ":7777" {
		t.Errorf("HTTPAddr = %q, флаг должен побеждать переменную окружения", cfg.HTTPAddr)
	}
	if cfg.BaseURL != "http://localhost:7777" {
		t.Errorf("BaseURL = %q, должен выводиться из итогового адреса", cfg.BaseURL)
	}
}

func TestLoadEnvUsedWhenNoFlag(t *testing.T) {
	env := envMap(map[string]string{
		"STORAGE":      "postgres",
		"POSTGRES_DSN": "postgres://user:pass@db:5432/links",
		"MEMORY_TTL":   "30m",
		"LOG_LEVEL":    "debug",
	})

	cfg, err := Load(nil, env, io.Discard)
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
	if cfg.Storage != StoragePostgres {
		t.Errorf("Storage = %q", cfg.Storage)
	}
	if cfg.MemoryTTL != 30*time.Minute {
		t.Errorf("MemoryTTL = %v", cfg.MemoryTTL)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
}

// Проверяем не только факт ошибки, но и то, что в сообщении названо поле
func TestLoadRejectsBadInput(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		contains string
	}{
		{"неизвестное хранилище", []string{"-storage", "sqlite"}, nil, "неизвестное хранилище"},
		{"postgres без DSN", []string{"-storage", "postgres"}, nil, "postgres-dsn"},
		{"некорректный уровень логирования", []string{"-log-level", "verbose"}, nil, "log-level"},
		{"отрицательный предел памяти", []string{"-memory-max-entries", "-1"}, nil, "memory-max-entries"},
		{"нулевой таймаут запроса", []string{"-request-timeout", "0s"}, nil, "request-timeout"},
		{"битая длительность в окружении", nil, map[string]string{"MEMORY_TTL": "полчаса"}, "MEMORY_TTL"},
		{"битое число в окружении", nil, map[string]string{"MEMORY_MAX_ENTRIES": "много"}, "MEMORY_MAX_ENTRIES"},
		{"базовый адрес без схемы", []string{"-base-url", "localhost:8081"}, nil, "base-url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := noEnv
			if tt.env != nil {
				env = envMap(tt.env)
			}

			_, err := Load(tt.args, env, io.Discard)
			if err == nil {
				t.Fatal("ожидалась ошибка, получен nil")
			}
			if !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("сообщение %q не содержит %q — оператору будет непонятно, что чинить",
					err.Error(), tt.contains)
			}
		})
	}
}

func TestLoadTrimsTrailingSlashInBaseURL(t *testing.T) {
	cfg, err := Load([]string{"-base-url", "https://sh.example.com/"}, noEnv, io.Discard)
	if err != nil {
		t.Fatalf("Load вернул ошибку: %v", err)
	}
	if cfg.BaseURL != "https://sh.example.com" {
		t.Errorf("BaseURL = %q, завершающий слэш должен быть убран", cfg.BaseURL)
	}
}

// Package storage выбирает реализацию хранилища по конфигурации
package storage

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/1RAFTIK1/url-shortener-oz/internal/config"
	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
	"github.com/1RAFTIK1/url-shortener-oz/internal/storage/memory"
	"github.com/1RAFTIK1/url-shortener-oz/internal/storage/postgres"
)

// Storage репозиторий домена плюс проверка
// готовности и корректное закрыти
type Storage interface {
	shortener.Repository
	Ping(ctx context.Context) error
	io.Closer
}

// New создаёт хранилище согласно конфигурации
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (Storage, error) {
	switch cfg.Storage {
	case config.StorageMemory:
		return memory.New(
			memory.WithMaxEntries(cfg.MemoryMaxEntries),
			memory.WithTTL(cfg.MemoryTTL),
		), nil

	case config.StoragePostgres:
		return postgres.New(ctx, cfg.PostgresDSN, log)

	default:
		return nil, fmt.Errorf("неизвестное хранилище %q", cfg.Storage)
	}
}

//go:build integration

// Тесты этого файла поднимают настоящий PostgreSQL в контейнере
package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpg "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
	"github.com/1RAFTIK1/url-shortener-oz/internal/storage/postgres"
)

func newStorage(t *testing.T) *postgres.Storage {
	t.Helper()

	ctx := context.Background()

	container, err := tcpg.Run(ctx, "postgres:18-alpine",
		tcpg.WithDatabase("links"),
		tcpg.WithUsername("test"),
		tcpg.WithPassword("test"),
		// Postgres в образе стартует дважды
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	// CleanupContainer гасит контейнер даже если Run завершился ошибкой
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("запуск контейнера PostgreSQL: %v", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("строка подключения: %v", err)
	}

	store, err := postgres.New(ctx, dsn, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return store
}

func link(code, url string) shortener.Link {
	// Postgres хранит timestamptz с точностью до микросекунды, поэтому
	// сравнивать наносекундное время из Go напрямую нельзя
	return shortener.Link{Code: code, URL: url, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
}

func TestIntegrationSaveAndGet(t *testing.T) {
	store := newStorage(t)
	ctx := context.Background()

	want := link("aB3_x9Zq0k", "https://ozon.ru/a")

	stored, created, err := store.Save(ctx, want)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !created {
		t.Error("created = false, ожидалось true")
	}
	if stored.Code != want.Code || stored.URL != want.URL {
		t.Errorf("сохранено %+v, ожидалось %+v", stored, want)
	}
	// Время задаёт домен, а не сервер БД
	if !stored.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, ожидалось %v", stored.CreatedAt, want.CreatedAt)
	}

	got, err := store.GetByCode(ctx, want.Code)
	if err != nil {
		t.Fatalf("GetByCode: %v", err)
	}
	if got.URL != want.URL {
		t.Errorf("URL = %q, ожидался %q", got.URL, want.URL)
	}
}

func TestIntegrationDeduplicatesByURL(t *testing.T) {
	store := newStorage(t)
	ctx := context.Background()

	first, created, err := store.Save(ctx, link("aaaaaaaaaa", "https://ozon.ru/dedup"))
	if err != nil || !created {
		t.Fatalf("первый Save: created=%v err=%v", created, err)
	}

	second, created, err := store.Save(ctx, link("bbbbbbbbbb", "https://ozon.ru/dedup"))
	if err != nil {
		t.Fatalf("второй Save: %v", err)
	}
	if created {
		t.Error("created = true, ожидалось false: адрес уже сохранён")
	}
	if second.Code != first.Code {
		t.Errorf("вернулся код %q, ожидался существующий %q", second.Code, first.Code)
	}
	if _, err := store.GetByCode(ctx, "bbbbbbbbbb"); !errors.Is(err, shortener.ErrNotFound) {
		t.Error("второй код не должен был попасть в базу")
	}
}

// тесты ниже проверяют требования ТЗ на уровне базы
func TestIntegrationCodeCollisionIsDistinguishable(t *testing.T) {
	store := newStorage(t)
	ctx := context.Background()

	if _, _, err := store.Save(ctx, link("cccccccccc", "https://ozon.ru/first")); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	// Тот же код, но другой адрес
	_, _, err := store.Save(ctx, link("cccccccccc", "https://ozon.ru/second"))
	if !errors.Is(err, shortener.ErrCodeTaken) {
		t.Errorf("err = %v, ожидалась ErrCodeTaken", err)
	}
}

func TestIntegrationNotFound(t *testing.T) {
	store := newStorage(t)

	_, err := store.GetByCode(context.Background(), "zzzzzzzzzz")
	if !errors.Is(err, shortener.ErrNotFound) {
		t.Errorf("err = %v, ожидалась ErrNotFound", err)
	}
}

func TestIntegrationCheckConstraintsProtectTable(t *testing.T) {
	store := newStorage(t)
	ctx := context.Background()

	_, _, err := store.Save(ctx, link("короткий", "https://ozon.ru/bad-code"))
	if err == nil {
		t.Error("ожидалась ошибка: код не соответствует формату из CHECK-констрейнта")
	}
	// Нарушение CHECK не должно выглядеть как коллизия
	if errors.Is(err, shortener.ErrCodeTaken) {
		t.Errorf("нарушение CHECK не должно выглядеть как коллизия кода: %v", err)
	}
}

// Главная проверка требования ТЗ на уровне базы
func TestIntegrationConcurrentSaveOfSameURL(t *testing.T) {
	store := newStorage(t)
	ctx := context.Background()

	const goroutines = 100
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		codes   = make(map[string]int)
		created int
	)

	start := make(chan struct{})
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start

			l := link(fmt.Sprintf("conc%06d", i), "https://ozon.ru/concurrent")
			stored, wasCreated, err := store.Save(ctx, l)
			if err != nil {
				t.Errorf("Save: %v", err)
				return
			}

			mu.Lock()
			codes[stored.Code]++
			if wasCreated {
				created++
			}
			mu.Unlock()
		}(i)
	}

	close(start)
	wg.Wait()

	if len(codes) != 1 {
		t.Errorf("получено %d различных кодов, ожидался ровно один: %v", len(codes), codes)
	}
	if created != 1 {
		t.Errorf("created = true у %d горутин, ожидалась ровно одна", created)
	}
}

func TestIntegrationContextCancellation(t *testing.T) {
	store := newStorage(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.GetByCode(ctx, "aaaaaaaaaa"); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, ожидалась context.Canceled", err)
	}
}

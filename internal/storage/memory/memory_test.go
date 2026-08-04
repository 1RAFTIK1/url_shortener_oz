package memory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

// fakeClock хранит время в атомарном счётчике часы читает горутина-дворник,
// а двигает их тест, и без atomic это была бы гонка
type fakeClock struct{ nanos atomic.Int64 }

func newFakeClock(t time.Time) *fakeClock {
	c := &fakeClock{}
	c.nanos.Store(t.UnixNano())
	return c
}

func (c *fakeClock) Now() time.Time          { return time.Unix(0, c.nanos.Load()) }
func (c *fakeClock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

func link(code, url string) shortener.Link {
	return shortener.Link{Code: code, URL: url, CreatedAt: time.Now()}
}

func TestSaveAndGet(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	stored, created, err := s.Save(ctx, link("aaaaaaaaaa", "https://example.com/a"))
	if err != nil {
		t.Fatalf("Save вернул ошибку: %v", err)
	}
	if !created {
		t.Error("created = false, ожидалось true")
	}
	if stored.Code != "aaaaaaaaaa" {
		t.Errorf("Code = %q", stored.Code)
	}

	got, err := s.GetByCode(ctx, "aaaaaaaaaa")
	if err != nil {
		t.Fatalf("GetByCode вернул ошибку: %v", err)
	}
	if got.URL != "https://example.com/a" {
		t.Errorf("URL = %q", got.URL)
	}
}

func TestSaveDeduplicatesByURL(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	first, _, err := s.Save(ctx, link("aaaaaaaaaa", "https://example.com/a"))
	if err != nil {
		t.Fatalf("первый Save: %v", err)
	}

	second, created, err := s.Save(ctx, link("bbbbbbbbbb", "https://example.com/a"))
	if err != nil {
		t.Fatalf("второй Save: %v", err)
	}
	if created {
		t.Error("created = true, ожидалось false")
	}
	if second.Code != first.Code {
		t.Errorf("вернулся код %q, ожидался существующий %q", second.Code, first.Code)
	}
	if n := s.Len(); n != 1 {
		t.Errorf("Len() = %d, ожидалась одна запись", n)
	}
	// Отвергнутый код не должен осесть в хранилище
	if _, err := s.GetByCode(ctx, "bbbbbbbbbb"); !errors.Is(err, shortener.ErrNotFound) {
		t.Error("второй код не должен был сохраниться")
	}
}

func TestSaveReportsCodeCollision(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	if _, _, err := s.Save(ctx, link("aaaaaaaaaa", "https://example.com/a")); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	_, _, err := s.Save(ctx, link("aaaaaaaaaa", "https://example.com/other"))
	if !errors.Is(err, shortener.ErrCodeTaken) {
		t.Errorf("err = %v, ожидалась ErrCodeTaken", err)
	}
}

func TestGetByCodeNotFound(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	_, err := s.GetByCode(context.Background(), "zzzzzzzzzz")
	if !errors.Is(err, shortener.ErrNotFound) {
		t.Errorf("err = %v, ожидалась ErrNotFound", err)
	}
}

func TestSaveRespectsCapacity(t *testing.T) {
	s := New(WithMaxEntries(2))
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	for i := range 2 {
		l := link(fmt.Sprintf("code%06d", i), fmt.Sprintf("https://example.com/%d", i))
		if _, _, err := s.Save(ctx, l); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}

	_, _, err := s.Save(ctx, link("overflowaa", "https://example.com/overflow"))
	if !errors.Is(err, shortener.ErrStorageFull) {
		t.Errorf("err = %v, ожидалась ErrStorageFull", err)
	}
	// Переполнение не должно ломать уже сохранённое.
	if _, err := s.GetByCode(ctx, "code000000"); err != nil {
		t.Errorf("существующие записи должны остаться доступны: %v", err)
	}
}

func TestExpiredLinkIsNotReturned(t *testing.T) {
	clock := newFakeClock(time.Now())
	s := New(WithTTL(time.Hour), WithCleanupInterval(time.Hour), withClock(clock.Now))
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	l := shortener.Link{Code: "aaaaaaaaaa", URL: "https://example.com/a", CreatedAt: clock.Now()}
	if _, _, err := s.Save(ctx, l); err != nil {
		t.Fatalf("подготовка: %v", err)
	}

	clock.Advance(time.Hour + time.Second)

	if _, err := s.GetByCode(ctx, "aaaaaaaaaa"); !errors.Is(err, shortener.ErrNotFound) {
		t.Errorf("err = %v, протухшая ссылка не должна возвращаться", err)
	}

	// Протухшая запись не должна мешать сохранить тот же адрес заново
	// иначе адрес оказался бы заблокирован навсегда
	stored, created, err := s.Save(ctx, shortener.Link{
		Code: "bbbbbbbbbb", URL: "https://example.com/a", CreatedAt: clock.Now(),
	})
	if err != nil {
		t.Fatalf("повторное сохранение после истечения TTL: %v", err)
	}
	if !created || stored.Code != "bbbbbbbbbb" {
		t.Errorf("created=%v code=%q — протухшая запись должна была замениться новой", created, stored.Code)
	}
}

// Проверяем не «истёк ли TTL», а то, что память действительно освобождается
func TestJanitorFreesMemory(t *testing.T) {
	clock := newFakeClock(time.Now())
	s := New(WithTTL(time.Hour), WithCleanupInterval(5*time.Millisecond), withClock(clock.Now))
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	for i := range 100 {
		l := shortener.Link{
			Code:      fmt.Sprintf("code%06d", i),
			URL:       fmt.Sprintf("https://example.com/%d", i),
			CreatedAt: clock.Now(),
		}
		if _, _, err := s.Save(ctx, l); err != nil {
			t.Fatalf("подготовка %d: %v", i, err)
		}
	}
	if s.Len() != 100 {
		t.Fatalf("Len() = %d, ожидалось 100", s.Len())
	}

	clock.Advance(2 * time.Hour)

	// Опрос с крайним сроком вместо фиксированного sleep
	deadline := time.Now().Add(2 * time.Second)
	for s.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := s.Len(); n != 0 {
		t.Errorf("Len() = %d, дворник должен был освободить все записи", n)
	}
}

func TestCloseIsIdempotentAndStopsJanitor(t *testing.T) {
	s := New(WithTTL(time.Hour), WithCleanupInterval(time.Millisecond))

	if err := s.Close(); err != nil {
		t.Fatalf("первый Close: %v", err)
	}
	// Двойной Close случается при обработке ошибок в main — паниковать нельзя
	if err := s.Close(); err != nil {
		t.Fatalf("второй Close не должен паниковать или возвращать ошибку: %v", err)
	}
}

func TestContextCancellationIsRespected(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := s.Save(ctx, link("aaaaaaaaaa", "https://example.com/a")); !errors.Is(err, context.Canceled) {
		t.Errorf("Save: err = %v, ожидалась context.Canceled", err)
	}
	if _, err := s.GetByCode(ctx, "aaaaaaaaaa"); !errors.Is(err, context.Canceled) {
		t.Errorf("GetByCode: err = %v, ожидалась context.Canceled", err)
	}
}

// сто горутин одновременно сохраняют один адрес под разными кодами
func TestConcurrentSaveOfSameURLYieldsSingleCode(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	const goroutines = 100
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results = make(map[string]int)
		created atomic.Int64
	)

	// все горутины бьют в хранилище одновременно,
	start := make(chan struct{})
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start

			l := shortener.Link{
				Code:      fmt.Sprintf("code%06d", i),
				URL:       "https://example.com/same",
				CreatedAt: time.Now(),
			}
			stored, wasCreated, err := s.Save(ctx, l)
			if err != nil {
				t.Errorf("Save: %v", err)
				return
			}
			if wasCreated {
				created.Add(1)
			}
			mu.Lock()
			results[stored.Code]++
			mu.Unlock()
		}(i)
	}

	close(start)
	wg.Wait()

	if len(results) != 1 {
		t.Errorf("получено %d различных кодов, ожидался ровно один: %v", len(results), results)
	}
	if n := created.Load(); n != 1 {
		t.Errorf("created = true у %d горутин, ожидалась ровно одна", n)
	}
	if n := s.Len(); n != 1 {
		t.Errorf("Len() = %d, ожидалась одна запись", n)
	}
}

func TestConcurrentSaveOfDistinctURLs(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	const goroutines = 500
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := shortener.Link{
				Code:      fmt.Sprintf("code%06d", i),
				URL:       fmt.Sprintf("https://example.com/%d", i),
				CreatedAt: time.Now(),
			}
			if _, _, err := s.Save(ctx, l); err != nil {
				t.Errorf("Save %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if n := s.Len(); n != goroutines {
		t.Errorf("Len() = %d, ожидалось %d", n, goroutines)
	}
	for i := range goroutines {
		if _, err := s.GetByCode(ctx, fmt.Sprintf("code%06d", i)); err != nil {
			t.Fatalf("запись %d потерялась: %v", i, err)
		}
	}
}

// Смешанная нагрузка на чтение и запись
func TestConcurrentReadsAndWrites(t *testing.T) {
	s := New()
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := range 200 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			l := shortener.Link{
				Code:      fmt.Sprintf("code%06d", i),
				URL:       fmt.Sprintf("https://example.com/%d", i),
				CreatedAt: time.Now(),
			}
			_, _, _ = s.Save(ctx, l)
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = s.GetByCode(ctx, fmt.Sprintf("code%06d", i))
		}(i)
	}
	wg.Wait()
}

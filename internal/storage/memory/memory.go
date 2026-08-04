// Package memory хранит ссылки в памяти процесса
// Это собственная реализация хранилища
// Данные живут только пока живёт процес поэтому у хранилища есть
// предел ёмкости и необязательный TTL
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

const (
	// DefaultMaxEntries — примерно 250 МБ при средней длине адреса ~80 байт
	// (ключ, значение и накладные расходы обеих карт)
	DefaultMaxEntries = 1_000_000

	minCleanupInterval = time.Second
	maxCleanupInterval = time.Minute
)

var _ shortener.Repository = (*Storage)(nil)

// Storage потокобезопасное хранилище ссылок в памяти
// Индексов два по коду и по адресу. Оба под одним мьютексом, потому что
// операция сохранения обязана проверять и изменять их атомарно иначе два
// одновременных запроса на один адрес создали бы две разные короткие ссылки
// По этой же причине здесь не подходит sync.Map он даёт атомарность
// операций над одной картой, но не над двумя сразу
type Storage struct {
	mu     sync.RWMutex
	byCode map[string]shortener.Link
	byURL  map[string]string // адрес -> код, чтобы не дублировать структуру

	maxEntries      int
	ttl             time.Duration
	cleanupInterval time.Duration
	now             func() time.Time

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// Option настраивает хранилище Функциональные опции выбраны, чтобы добавление
// нового параметра не ломало сигнатуру New у всех вызывающих
type Option func(*Storage)

// WithMaxEntries ограничивает число хранимых ссылок Ноль снимает предел
func WithMaxEntries(n int) Option {
	return func(s *Storage) { s.maxEntries = n }
}

// WithTTL включает срок жизни ссылок и фоновую очистку Ноль бессрочно
func WithTTL(d time.Duration) Option {
	return func(s *Storage) { s.ttl = d }
}

// WithCleanupInterval задаёт период работы дворника
func WithCleanupInterval(d time.Duration) Option {
	return func(s *Storage) { s.cleanupInterval = d }
}

// withClock подменяет часы в тестах
func withClock(now func() time.Time) Option {
	return func(s *Storage) { s.now = now }
}

// New создаёт хранилище
func New(opts ...Option) *Storage {
	s := &Storage{
		byCode:     make(map[string]shortener.Link),
		byURL:      make(map[string]string),
		maxEntries: DefaultMaxEntries,
		now:        time.Now,
		stop:       make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}

	if s.ttl > 0 {
		if s.cleanupInterval <= 0 {
			s.cleanupInterval = defaultCleanupInterval(s.ttl)
		}
		s.wg.Add(1)
		go s.janitor()
	}

	return s
}

func defaultCleanupInterval(ttl time.Duration) time.Duration {
	interval := ttl / 10
	if interval < minCleanupInterval {
		return minCleanupInterval
	}
	if interval > maxCleanupInterval {
		return maxCleanupInterval
	}
	return interval
}

// Save атомарно сохраняет ссылку
func (s *Storage) Save(ctx context.Context, l shortener.Link) (shortener.Link, bool, error) {
	// Операция мгновенная, но контракт интерфейса обязывает уточнять контекст
	if err := ctx.Err(); err != nil {
		return shortener.Link{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if code, ok := s.byURL[l.URL]; ok {
		existing := s.byCode[code]
		if !s.expired(existing) {
			return existing, false, nil
		}
		s.remove(code, l.URL) // протухшую запись считаем отсутствующей
	}

	if existing, ok := s.byCode[l.Code]; ok {
		if !s.expired(existing) {
			return shortener.Link{}, false, shortener.ErrCodeTaken
		}
		s.remove(existing.Code, existing.URL)
	}

	if s.maxEntries > 0 && len(s.byCode) >= s.maxEntries {
		return shortener.Link{}, false, shortener.ErrStorageFull
	}

	s.byCode[l.Code] = l
	s.byURL[l.URL] = l.Code
	return l, true, nil
}

// GetByCode возвращает ссылку по коду
func (s *Storage) GetByCode(ctx context.Context, code string) (shortener.Link, error) {
	if err := ctx.Err(); err != nil {
		return shortener.Link{}, err
	}

	s.mu.RLock()
	link, ok := s.byCode[code]
	s.mu.RUnlock()

	// Протухшую запись не удаляем прямо здесь Её уберёт дворник, а до тех пор
	// она просто не отдаётся наружу
	if !ok || s.expired(link) {
		return shortener.Link{}, shortener.ErrNotFound
	}
	return link, nil
}

// Ping реализует проверку готовности
func (s *Storage) Ping(context.Context) error { return nil }

// Len возвращает число хранимых ссылок для тестов
func (s *Storage) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byCode)
}

// Close останавливает дворника и дожидается его завершения
func (s *Storage) Close() error {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
	return nil
}

func (s *Storage) expired(l shortener.Link) bool {
	return s.ttl > 0 && s.now().Sub(l.CreatedAt) >= s.ttl
}

func (s *Storage) remove(code, url string) {
	delete(s.byCode, code)
	delete(s.byURL, url)
}

func (s *Storage) janitor() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.deleteExpired()
		}
	}
}

// deleteExpired проходит по всем записям под эксклюзивной блокировкой
func (s *Storage) deleteExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	var removed int
	// Удаление из карты во время range по ней — определённое поведение в Go
	for code, link := range s.byCode {
		if s.expired(link) {
			s.remove(code, link.URL)
			removed++
		}
	}
	return removed
}

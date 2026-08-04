// Package postgres реализует хранилище ссылок поверх PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

const (
	// Имя первичного ключа из миграции
	constraintCodePK = "links_pkey"

	uniqueViolation     = "23505"
	checkViolation      = "23514"
	connectionExcPrefix = "08" // класс ошибок соединения по SQLSTATE
)

var _ shortener.Repository = (*Storage)(nil)

// Storage хранилище ссылок в PostgreSQL
type Storage struct {
	pool *pgxpool.Pool
}

// New открывает пул соединений, проверяет доступность базы и применяет миграции
func New(ctx context.Context, dsn string, log *slog.Logger) (*Storage, error) {
	poolCfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("разбор строки подключения: %w", err)
	}

	// Настройки пула
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 1
	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("создание пула соединений: %w", err)
	}

	// pgxpool.NewWithConfig соединение не устанавливает
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("проверка соединения с базой: %w", err)
	}

	if err := migrate(ctx, pool, log); err != nil {
		pool.Close()
		return nil, fmt.Errorf("миграции: %w", err)
	}

	return &Storage{pool: pool}, nil
}

// Save вставляет ссылку одним атомарным оператором.
//
// ON CONFLICT ... DO NOTHING, а не DO UPDATE: DO UPDATE вернул бы строку одним
// запросом, но записывал бы мёртвый кортеж на каждый повтор, а значит раздувал
// таблицу и индекс на сервисе, который живёт годами. Здесь повтор стоит одного
// дополнительного SELECT, зато только на редком пути.
func (s *Storage) Save(ctx context.Context, l shortener.Link) (shortener.Link, bool, error) {
	const insert = `
		INSERT INTO links (code, original_url, created_at)
		VALUES ($1, $2, $3)
		ON CONFLICT ON CONSTRAINT links_original_url_key DO NOTHING
		RETURNING code, original_url, created_at`

	var stored shortener.Link
	err := s.pool.QueryRow(ctx, insert, l.Code, l.URL, l.CreatedAt).
		Scan(&stored.Code, &stored.URL, &stored.CreatedAt)
	// pgx декодирует timestamptz в часовом поясе сессии
	stored.CreatedAt = stored.CreatedAt.UTC()

	switch {
	case err == nil:
		return stored, true, nil

	case errors.Is(err, pgx.ErrNoRows):
		// Строк нет — значит сработал конфликт по адресу
		existing, err := s.getByURL(ctx, l.URL)
		if err != nil {
			// Пусто и здесь бывает, если победитель откатился
			return shortener.Link{}, false, err
		}
		return existing, false, nil

	default:
		// Единственный оставшийся конфликт по первичному ключу
		return shortener.Link{}, false, mapError(err)
	}

}

func (s *Storage) getByURL(ctx context.Context, url string) (shortener.Link, error) {
	const query = `
		SELECT code, original_url, created_at
		FROM links
		WHERE original_url = $1`

	var l shortener.Link
	err := s.pool.QueryRow(ctx, query, url).Scan(&l.Code, &l.URL, &l.CreatedAt)
	// pgx декодирует timestamptz в часовом поясе сессии. Момент времени тот же,
	// но представление зависело бы от настроек сервера БД, а домен обещает UTC.
	l.CreatedAt = l.CreatedAt.UTC()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shortener.Link{}, shortener.ErrNotFound
		}
		return shortener.Link{}, mapError(err)
	}
	return l, nil
}

// GetByCode ищет ссылку по первичному ключу.
func (s *Storage) GetByCode(ctx context.Context, code string) (shortener.Link, error) {
	const query = `
		SELECT code, original_url, created_at
		FROM links
		WHERE code = $1`

	var l shortener.Link
	err := s.pool.QueryRow(ctx, query, code).Scan(&l.Code, &l.URL, &l.CreatedAt)
	l.CreatedAt = l.CreatedAt.UTC()
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return shortener.Link{}, shortener.ErrNotFound
		}
		return shortener.Link{}, mapError(err)
	}
	return l, nil
}

// Ping проверяет доступность базы для /readyz
func (s *Storage) Ping(ctx context.Context) error {
	if err := s.pool.Ping(ctx); err != nil {
		return mapError(err)
	}
	return nil
}

// Close закрывает пул. Возвращает error, чтобы удовлетворять io.Closer
func (s *Storage) Close() error {
	s.pool.Close()
	return nil
}

// mapError переводит ошибки драйвера в словарь домена
func mapError(err error) error {
	if err == nil {
		return nil
	}
	// Отмену и таймаут пропускаем как есть
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == uniqueViolation && pgErr.ConstraintName == constraintCodePK:
			return shortener.ErrCodeTaken
		case pgErr.Code == checkViolation:
			return fmt.Errorf("нарушено ограничение %s: %w", pgErr.ConstraintName, err)
		case strings.HasPrefix(pgErr.Code, connectionExcPrefix):
			return fmt.Errorf("%w: %w", shortener.ErrStorageUnavailable, err)
		case pgErr.Code == "57P01" || pgErr.Code == "57P03":
			// admin_shutdown и cannot_connect_now: база перезапускается.
			return fmt.Errorf("%w: %w", shortener.ErrStorageUnavailable, err)
		}
		return err
	}

	return fmt.Errorf("%w: %w", shortener.ErrStorageUnavailable, err)
}

package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Миграции встроены в бинарник
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

func migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	// goose ожидает миграции в корне переданной ФС, поэтому спускаемся
	// в подкаталог, а не отдаём весь embed целиком
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("подкаталог миграций: %w", err)
	}

	// goose работает через database/sql, поэтому оборачиваем pgxpool в *sql.DB.
	// Закрывать его не нужно, но
	// для соблюдения контракта defer'им.
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	// Сессионная блокировка на advisory-локе
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("блокировщик миграций: %w", err)
	}

	provider, err := goose.NewProvider(goose.DialectPostgres, db, sub,
		goose.WithSessionLocker(locker),
	)
	if err != nil {
		return fmt.Errorf("провайдер goose: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("применение миграций: %w", err)
	}

	for _, r := range results {
		log.Info("применена миграция",
			"version", r.Source.Version, "source", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

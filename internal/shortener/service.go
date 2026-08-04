package shortener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const defaultMaxAttempts = 3

// Service реализует сценарии сокращателя
type Service struct {
	repo        Repository
	gen         Generator
	log         *slog.Logger
	maxAttempts int
}

// NewService связывает зависимости
func NewService(repo Repository, gen Generator, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		repo:        repo,
		gen:         gen,
		log:         log,
		maxAttempts: defaultMaxAttempts,
	}
}

// Shorten возвращает ссылку для rawURL, создавая её, если такой ещё нет
// Второе значение сообщает, создана ли ссылка этим вызовом по нему транспорт
// отвечает 201 Created или 200 OK.
func (s *Service) Shorten(ctx context.Context, rawURL string) (Link, bool, error) {
	normalized, err := ParseAndNormalize(rawURL)
	if err != nil {
		return Link{}, false, err
	}

	var lastErr error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		code, err := s.gen.Generate()
		if err != nil {
			return Link{}, false, fmt.Errorf("generate code: %w", err)
		}
		stored, created, err := s.repo.Save(ctx, Link{
			Code:      code,
			URL:       normalized,
			CreatedAt: time.Now().UTC(),
		})
		switch {
		case err == nil:
			return stored, created, nil
		case errors.Is(err, ErrCodeTaken):
			lastErr = err
			s.log.WarnContext(ctx, "shortcode collision retrying", "attempt", attempt, "max_attempts", s.maxAttempts)
		default:
			return Link{}, false, fmt.Errorf("save link: %w", err)
		}
	}
	return Link{}, false, fmt.Errorf("%w after %d attempts: %w", ErrNoFreeCode, s.maxAttempts, lastErr)
}

// Resolve возвращает ссылку по коду либо ErrNotFound
func (s *Service) Resolve(ctx context.Context, code string) (Link, error) {
	if err := ValidateCode(code); err != nil {
		return Link{}, err
	}
	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return Link{}, fmt.Errorf("resolve %q: %w", code, err)
	}
	return link, nil
}

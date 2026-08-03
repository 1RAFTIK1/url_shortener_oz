package shortener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const defaultMaxAttempts = 3

type Service struct {
	repo Repository
	gen  Generator
	log  *slog.Logger
	maxAttempts int
}

func NewService(repo Repository, gen Generator, log *slog.Logger) *Service {
	if log == nil{
        log = slog.Default()
	}
	return &Service{
		repo:	 	 repo,
		gen:	 	 gen,
		log:  	 	 log,
		maxAttempts: defaultMaxAttempts,
	}
}

func (s *Service) Shorten(ctx context.Context, rawURL string) (Link, bool, error){
	normalized, err := ParseAndNormalize(rawURL)
	if err != nil {
		return Link{}, false, err
	}

	var lastErr error
	for attemp := 1; attemp <= s.maxAttempts; attemp++ {
		code, err := s.gen.Generate()
		if err != nil {
			return Link{}, false, fmt.Errorf("generate code: %w", err)
		}
	stored, created, err := s.repo.Save(ctx, Link{
		Code: code,
		URL: normalized,
		CreatedAt: time.Now().UTC(),
	})
	switch{
	case err == nil:
		return stored, created, nil
	case errors.Is(err, ErrCodeTaken):
		lastErr = err
		s.log.WarnContext(ctx, "shortcode collision retrying", "attempt", attemp, "max_attempts", s.maxAttempts)
		default:
			return Link{}, false, fmt.Errorf("save link: %w", err)
		}
	}
	return Link{}, false, fmt.Errorf("%w after %d attempts: %w", ErrNoFreeCode, s.maxAttempts, lastErr)
}

func (s *Service) Resolve(ctx context.Context, code string) (Link, error){
	if err := ValidateCode(code); err != nil {
		return Link{}, err
	}
	link, err := s.repo.GetByCode(ctx, code)
	if err != nil {
		return Link{}, fmt.Errorf("resolve %q: %w", code, err)
	}
	return link, nil
}
package shortener

import "context"

type Repository interface {
	Save(ctx context.Context, l Link) (stored Link, created bool, err error)
	GetByCode(ctx context.Context, code string) (Link, error)
}
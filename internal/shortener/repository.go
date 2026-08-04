package shortener

import "context"

// Repository хранит ссылки Все реализации обязаны быть безопасны при
// одновременном использовании несколькими горутинами
type Repository interface {
	Save(ctx context.Context, l Link) (stored Link, created bool, err error)
	GetByCode(ctx context.Context, code string) (Link, error)
}

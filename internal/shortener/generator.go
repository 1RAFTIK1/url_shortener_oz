package shortener

// Generator порождает короткие коды
// Уникальность обеспечивает уникальный индекс хранилища
type Generator interface {
	Generate() (string, error)
}

// GeneratorFunc позволяет обычной функции реализовать Generator
type GeneratorFunc func() (string, error)

// Generate вызывает f
func (f GeneratorFunc) Generate() (string, error) {
	return f()
}

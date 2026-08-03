package shortener

type Generator interface {
	Generate() (string, error)
}

type GeneratorFunc func() (string, error)

func (f GeneratorFunc) Generate() (string, error) {
	return f()
}
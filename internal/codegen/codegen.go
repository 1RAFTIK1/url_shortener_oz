// Package codegen генерирует короткие коды ссылок
package codegen

import (
	"crypto/rand"
	"fmt"
	"io"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

const (
	// alphabetSize 63 символа
	alphabetSize = len(shortener.Alphabet)

	// maxUnbiasedByte — наибольшее кратное alphabetSize, помещающееся в байт,
	// то есть 252. Байты 252..255 отбрасываются, и тогда остаток от деления
	// строго равномерен. Цена — 4/256 = 1.56% отброшенных байтов
	maxUnbiasedByte = 256 - (256 % alphabetSize)

	// maxReads ограничивает число чтений из источника случайности. При работе
	// с crypto/rand этот предел недостижим, он
	// существует, чтобы вырожденный источник приводил к ошибке, а не к вечному
	// циклу внутри HTTP-запроса.
	maxReads = 16
)

// Проверка на этапе компиляции: если сигнатура Generate разойдётся с
// интерфейсом домена, сборка упадёт здесь, а не в месте использования.
var _ shortener.Generator = (*Generator)(nil)

// Generator выдаёт коды, читая байты из криптографического источника.
// Значение не хранит состояния между вызовами, поэтому безопасно для
// одновременного использования несколькими горутинами.
type Generator struct {
	src    io.Reader
	length int
}

// New возвращает генератор поверх crypto/rand.
func New() *Generator {
	return &Generator{src: rand.Reader, length: shortener.CodeLength}
}

// newWithSource нужен тестам: подменяя источник, можно проверить и отбрасывание
// смещённых байтов, и проброс ошибки чтения. Неэкспортируемый намеренно —
// подменять источник случайности в проде незачем.
func newWithSource(src io.Reader) *Generator {
	return &Generator{src: src, length: shortener.CodeLength}
}

// Generate возвращает код из ровно shortener.CodeLength символов алфавита.
func (g *Generator) Generate() (string, error) {
	code := make([]byte, 0, g.length)
	buf := make([]byte, g.length)

	for reads := 0; len(code) < g.length; reads++ {
		if reads == maxReads {
			return "", fmt.Errorf(
				"codegen: источник случайности вернул %d блоков подряд без пригодных байтов",
				maxReads)
		}
		if _, err := io.ReadFull(g.src, buf); err != nil {
			return "", fmt.Errorf("codegen: чтение случайных байтов: %w", err)
		}
		for _, b := range buf {
			v := int(b)
			if v >= maxUnbiasedByte {
				continue // отбрасываем, иначе распределение будет смещённым
			}
			code = append(code, shortener.Alphabet[v%alphabetSize])
			if len(code) == g.length {
				break
			}
		}
	}

	return string(code), nil
}

package codegen

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

// Арифметика rejection sampling закреплена тестом если алфавит когда-нибудь
// изменится, тест напомнит пересчитать границу отбрасывания
func TestConstants(t *testing.T) {
	if alphabetSize != 63 {
		t.Fatalf("alphabetSize = %d, ожидалось 63", alphabetSize)
	}
	if maxUnbiasedByte != 252 {
		t.Errorf("maxUnbiasedByte = %d, ожидалось 252 (4*63)", maxUnbiasedByte)
	}
	if maxUnbiasedByte%alphabetSize != 0 {
		t.Errorf("maxUnbiasedByte=%d не кратен размеру алфавита %d — распределение будет смещено",
			maxUnbiasedByte, alphabetSize)
	}
}

// Форма кода проверяется тем же кодом
func TestGenerateShape(t *testing.T) {
	g := New()
	for i := 0; i < 1000; i++ {
		code, err := g.Generate()
		if err != nil {
			t.Fatalf("Generate вернул ошибку: %v", err)
		}
		if err := shortener.ValidateCode(code); err != nil {
			t.Fatalf("сгенерирован невалидный код %q: %v", code, err)
		}
	}
}

func TestGenerateUnique(t *testing.T) {
	n := 100_000
	if testing.Short() {
		n = 1_000
	}

	g := New()
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		code, err := g.Generate()
		if err != nil {
			t.Fatalf("Generate вернул ошибку на шаге %d: %v", i, err)
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("код %q повторился на шаге %d из %d", code, i, n)
		}
		seen[code] = struct{}{}
	}
}

// Проверка равномерности 200 000 символов, ожидание ~3175 на символ
func TestGenerateUniformity(t *testing.T) {
	const codes = 20_000

	g := New()
	counts := make(map[rune]int, alphabetSize)
	for i := 0; i < codes; i++ {
		code, err := g.Generate()
		if err != nil {
			t.Fatalf("Generate вернул ошибку: %v", err)
		}
		for _, r := range code {
			counts[r]++
		}
	}

	total := codes * shortener.CodeLength
	expected := float64(total) / float64(alphabetSize)
	const tolerance = 0.15

	for _, r := range shortener.Alphabet {
		got := counts[r]
		if got == 0 {
			t.Errorf("символ %q не встретился ни разу", r)
			continue
		}
		deviation := (float64(got) - expected) / expected
		if deviation > tolerance || deviation < -tolerance {
			t.Errorf("символ %q встретился %d раз при ожидаемых %.0f (отклонение %.1f%%)",
				r, got, expected, deviation*100)
		}
	}
}

// источник, который начинается ровно с четырёх «плохих» байтов
func TestGenerateRejectsBiasedBytes(t *testing.T) {
	src := bytes.NewReader(append(
		[]byte{252, 253, 254, 255},
		make([]byte, 20)...,
	))

	code, err := newWithSource(src).Generate()
	if err != nil {
		t.Fatalf("Generate вернул ошибку: %v", err)
	}
	if want := strings.Repeat(string(shortener.Alphabet[0]), shortener.CodeLength); code != want {
		t.Errorf("code = %q, ожидалось %q: байты 252..255 должны быть отброшены", code, want)
	}
}

func TestGenerateMapsBytesToAlphabet(t *testing.T) {
	src := bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9})

	code, err := newWithSource(src).Generate()
	if err != nil {
		t.Fatalf("Generate вернул ошибку: %v", err)
	}
	if want := shortener.Alphabet[:shortener.CodeLength]; code != want {
		t.Errorf("code = %q, ожидалось %q", code, want)
	}
}

func TestGeneratePropagatesReadError(t *testing.T) {
	wantErr := errors.New("источник случайности недоступен")

	_, err := newWithSource(errReader{wantErr}).Generate()
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, ожидалась исходная ошибка чтения", err)
	}
}

// Вырожденный источник не должен подвешивать вызывающую горутину
func TestGenerateGivesUpOnDegenerateSource(t *testing.T) {
	_, err := newWithSource(constReader(0xFF)).Generate()
	if err == nil {
		t.Fatal("ожидалась ошибка: источник не отдаёт ни одного пригодного байта")
	}
	if strings.Contains(err.Error(), "чтение случайных байтов") {
		t.Errorf("ошибка %v выглядит как ошибка чтения, ожидалась защита от зацикливания", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

type constReader byte

func (c constReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(c)
	}
	return len(p), nil
}

// sink
var sink string

func BenchmarkGenerate(b *testing.B) {
	g := New()
	for b.Loop() {
		code, err := g.Generate()
		if err != nil {
			b.Fatal(err)
		}
		sink = code
	}
}

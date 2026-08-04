package shortener

import (
	"errors"
	"strings"
	"testing"
)

func TestAlphabet(t *testing.T) {
	const want = 63
	if got := len(Alphabet); got != want {
		t.Errorf("len(Alphabet) = %d, want %d", got, want)
	}

	seen := make(map[rune]bool, len(Alphabet))
	for _, r := range Alphabet {
		if seen[r] {
			t.Errorf("duplicate rune %q in Alphabet", r)
		}
		seen[r] = true
	}

	for _, group := range []string{
		"abcdefghijklmnopqrstuvwxyz",
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"0123456789",
		"_",
	} {
		for _, r := range group {
			if !strings.ContainsRune(Alphabet, r) {
				t.Errorf("rune %q not in Alphabet", r)
			}
		}
	}
}

func TestAllowByteMatchesAlphabet(t *testing.T) {
	for i := range 256 {
		want := strings.ContainsRune(Alphabet, rune(i))
		if got := allowByte[i]; got != want {
			t.Errorf("allowByte[%d] = %t, want %t", i, got, want)
		}
	}
}

func TestValidateCode(t *testing.T) {
	tests := []struct {
		name string
		code string
		want error
	}{
		{"типичный код", "aB3_x9Zq0k", nil},
		{"только подчёркивания", "__________", nil},
		{"только цифры", "0123456789", nil},
		{"граница алфавита", "azAZ09__aA", nil},

		{"пустая строка", "", ErrInvalidCode},
		{"на символ короче", "aB3_x9Zq0", ErrInvalidCode},
		{"на символ длиннее", "aB3_x9Zq0kL", ErrInvalidCode},
		{"дефис вместо подчёркивания", "aB3-x9Zq0k", ErrInvalidCode},
		{"пробел внутри", "aB3 x9Zq0k", ErrInvalidCode},
		{"слэш — попытка выйти за пределы пути", "aB3/x9Zq0k", ErrInvalidCode},
		{"процент — попытка percent-encoding", "aB3%x9Zq0k", ErrInvalidCode},
		{"нулевой байт", "aB3\x00x9Zq0k", ErrInvalidCode},
		{"кириллица длиной ровно 10 байт", "абвгд", ErrInvalidCode},
		{"десять эмодзи", strings.Repeat("🙂", 10), ErrInvalidCode},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateCode(tt.code); !errors.Is(got, tt.want) {
				t.Errorf("ValidateCode(%q) = %v, ожидалось %v", tt.code, got, tt.want)
			}
		})
	}
}

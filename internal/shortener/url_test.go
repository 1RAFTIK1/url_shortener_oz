package shortener

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error // nil адрес принимается
	}{
		{"обычный https", "https://example.com/a", nil},
		{"http тоже допустим", "http://example.com", nil},
		{"с портом и запросом", "https://example.com:8443/a?b=1", nil},
		{"пробелы по краям обрезаются", "  https://example.com/a  ", nil},
		{"длина ровно на границе", "https://example.com/" + strings.Repeat("a", MaxURLLength-20), nil},

		{"пустая строка", "", ErrInvalidURL},
		{"только пробелы", "   \t\n ", ErrInvalidURL},
		{"относительный адрес без схемы", "example.com/a", ErrInvalidURL},
		{"схема ftp", "ftp://example.com", ErrInvalidURL},
		{"схема javascript", "javascript:alert(1)", ErrInvalidURL},
		{"схема data", "data:text/html,<script>x</script>", ErrInvalidURL},
		{"пустой хост", "https://", ErrInvalidURL},
		{"учётные данные в адресе", "https://user:pass@bank.example.com/", ErrInvalidURL},
		{"управляющий символ внутри", "https://example.com/a\nb", ErrInvalidURL},
		{"длиннее предела", "https://example.com/" + strings.Repeat("a", MaxURLLength), ErrURLTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseURL(tt.raw)
			if !errors.Is(err, tt.want) {
				t.Errorf("parseURL(%.40q) = %v, ожидалось %v", tt.raw, err, tt.want)
			}
		})
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"хост в нижний регистр, путь не трогаем", "https://Example.COM/Path", "https://example.com/Path"},
		{"дефолтный порт https отбрасывается", "https://example.com:443/a", "https://example.com/a"},
		{"дефолтный порт http отбрасывается", "http://example.com:80/a", "http://example.com/a"},
		{"нестандартный порт сохраняется", "http://example.com:8080/a", "http://example.com:8080/a"},
		{"порт 443 на http — не дефолтный, сохраняется", "http://example.com:443/a", "http://example.com:443/a"},
		{"пустой путь становится слэшем", "https://example.com", "https://example.com/"},
		{"завершающий слэш сохраняется", "https://example.com/a/", "https://example.com/a/"},
		{"пустой фрагмент отбрасывается", "https://example.com/a#", "https://example.com/a"},
		{"непустой фрагмент сохраняется", "https://example.com/a#frag", "https://example.com/a#frag"},
		{"строка запроса сохраняется как есть", "https://example.com/a?b=1&B=2", "https://example.com/a?b=1&B=2"},
		{"IPv6 без порта", "http://[2001:db8::1]/a", "http://[2001:db8::1]/a"},
		{"IPv6 с портом", "http://[2001:db8::1]:8080/a", "http://[2001:db8::1]:8080/a"},
		{"IPv6 с дефолтным портом", "http://[2001:db8::1]:80/a", "http://[2001:db8::1]/a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.in)
			if err != nil {
				t.Fatalf("подготовка теста: url.Parse(%q) = %v", tt.in, err)
			}
			if got := normalizeURL(u); got != tt.want {
				t.Errorf("normalizeURL(%q) = %q, ожидалось %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNormalizeURLDoesNotMutateArgument(t *testing.T) {
	u, err := url.Parse("https://Example.COM:443")
	if err != nil {
		t.Fatalf("подготовка теста: %v", err)
	}
	before := *u

	normalizeURL(u)

	if *u != before {
		t.Errorf("normalizeURL изменила аргумент: было %+v, стало %+v", before, *u)
	}
}

func TestParseAndNormalizeEquivalence(t *testing.T) {
	groups := [][]string{
		{
			"https://example.com",
			"https://example.com/",
			"HTTPS://EXAMPLE.COM/",
			"https://example.com:443/",
			"  https://example.com/  ",
			"https://example.com/#",
		},
		{
			"http://example.com/a",
			"http://EXAMPLE.com:80/a",
		},
		{
			"http://example.com/a/",
		},
	}

	canonical := make([]string, len(groups))
	for i, group := range groups {
		for j, raw := range group {
			got, err := ParseAndNormalize(raw)
			if err != nil {
				t.Fatalf("группа %d: ParseAndNormalize(%q) вернула ошибку %v", i, raw, err)
			}
			if j == 0 {
				canonical[i] = got
				continue
			}
			if got != canonical[i] {
				t.Errorf("группа %d: %q дал %q, а %q дал %q — должны совпадать",
					i, raw, got, group[0], canonical[i])
			}
		}
	}

	for i := range canonical {
		for j := i + 1; j < len(canonical); j++ {
			if canonical[i] == canonical[j] {
				t.Errorf("группы %d и %d схлопнулись в один адрес %q, а должны различаться", i, j, canonical[i])
			}
		}
	}
}

package shortener

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ParseAndNormalize проверяет пользовательский адрес и возвращает его
// каноническую форму. Два адреса, приведённые к одной строке, считаются одной
// ссылкой — именно это придаёт смысл требованию ТЗ об уникальности.
func ParseAndNormalize(raw string) (string, error) {
	u, err := parseURL(raw)
	if err != nil {
		return "", err
	}
	return normalizeURL(u), nil
}

func parseURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: invalid url: empty string", ErrInvalidURL)
	}
	if len(raw) > MaxURLLength {
		return nil, ErrURLTooLong
	}

	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}

	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("%w: invalid url: unsupported scheme, scheme should be 'http' or 'https'", ErrInvalidURL)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: invalid url: user info is not allowed", ErrInvalidURL)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%w: invalid url: hostname is empty", ErrInvalidURL)
	}
	return u, nil
}

func normalizeURL(u *url.URL) string {
	n := *u

	n.Scheme = strings.ToLower(n.Scheme)

	host := strings.ToLower(n.Hostname())
	port := n.Port()

	if (n.Scheme == "http" && port == "80") || (n.Scheme == "https" && port == "443") {
		port = ""
	}
	switch {
	case port != "":
		n.Host = net.JoinHostPort(host, port)
	case strings.Contains(host, ":"):
		n.Host = "[" + host + "]"
	default:
		n.Host = host
	}
	if n.Path == "" {
		n.Path = "/"
	}
	return n.String()
}

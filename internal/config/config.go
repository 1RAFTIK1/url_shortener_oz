// Package config собирает конфигурацию из флагов и переменных окружения.
//
// флаг > переменная окружения > значение по умолчанию
package config

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// StorageKind вид хранилища
type StorageKind string

// Возможные значения StorageKind
const (
	StorageMemory   StorageKind = "memory"
	StoragePostgres StorageKind = "postgres"
)

// Config полностью проверенная конфигурация
type Config struct {
	HTTPAddr         string
	BaseURL          string
	Storage          StorageKind
	PostgresDSN      string
	MemoryMaxEntries int
	MemoryTTL        time.Duration
	LogLevel         slog.Level
	RequestTimeout   time.Duration
	ShutdownTimeout  time.Duration
	Healthcheck      bool
}

// LookupEnv повторяет сигнатуру os.LookupEnv
type LookupEnv func(string) (string, bool)

// Load разбирает аргументы и окружение
func Load(args []string, lookupEnv LookupEnv, out io.Writer) (Config, error) {
	env := &envReader{lookup: lookupEnv}

	// ContinueOnError вместо ExitOnErrorбиблиотечный код не должен сам
	// завершать процесс решение принимает main.
	fs := flag.NewFlagSet("shortener", flag.ContinueOnError)
	fs.SetOutput(out)

	var (
		cfg      Config
		storage  string
		logLevel string
	)

	fs.StringVar(&cfg.HTTPAddr, "http-addr", env.String("HTTP_ADDR", ":8081"),
		"адрес и порт HTTP-сервера")
	fs.StringVar(&cfg.BaseURL, "base-url", env.String("BASE_URL", ""),
		"базовый адрес коротких ссылок (по умолчанию выводится из -http-addr)")
	fs.StringVar(&storage, "storage", env.String("STORAGE", string(StorageMemory)),
		"хранилище: memory или postgres")
	fs.StringVar(&cfg.PostgresDSN, "postgres-dsn", env.String("POSTGRES_DSN", ""),
		"строка подключения к PostgreSQL")
	fs.IntVar(&cfg.MemoryMaxEntries, "memory-max-entries", env.Int("MEMORY_MAX_ENTRIES", 1_000_000),
		"предел числа ссылок в памяти, 0 — без предела")
	fs.DurationVar(&cfg.MemoryTTL, "memory-ttl", env.Duration("MEMORY_TTL", 0),
		"время жизни ссылки в памяти, 0 — бессрочно")
	fs.StringVar(&logLevel, "log-level", env.String("LOG_LEVEL", "info"),
		"уровень логирования: debug, info, warn, error")
	fs.DurationVar(&cfg.RequestTimeout, "request-timeout", env.Duration("REQUEST_TIMEOUT", 5*time.Second),
		"предельное время обработки одного запроса")
	fs.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", env.Duration("SHUTDOWN_TIMEOUT", 10*time.Second),
		"сколько ждать завершения активных запросов при остановке")
	fs.BoolVar(&cfg.Healthcheck, "healthcheck", false,
		"проверить живость запущенного сервиса и выйти (для HEALTHCHECK образа)")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	// Ошибки сообщаем все сразу
	if err := errors.Join(env.errs...); err != nil {
		return Config{}, err
	}

	cfg.Storage = StorageKind(storage)
	if err := cfg.LogLevel.UnmarshalText([]byte(logLevel)); err != nil {
		return Config{}, fmt.Errorf("некорректный -log-level %q: %w", logLevel, err)
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL(cfg.HTTPAddr)
	}
	// Слэш убираем один раз
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Storage {
	case StorageMemory, StoragePostgres:
	default:
		return fmt.Errorf("неизвестное хранилище %q: допустимы %q и %q",
			c.Storage, StorageMemory, StoragePostgres)
	}

	if c.Storage == StoragePostgres && c.PostgresDSN == "" {
		return errors.New("для -storage=postgres требуется -postgres-dsn (или POSTGRES_DSN)")
	}
	if c.HTTPAddr == "" {
		return errors.New("-http-addr не может быть пустым")
	}

	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("некорректный -base-url %q: ожидается вид http://host:port", c.BaseURL)
	}

	if c.MemoryMaxEntries < 0 {
		return errors.New("-memory-max-entries не может быть отрицательным")
	}
	if c.MemoryTTL < 0 {
		return errors.New("-memory-ttl не может быть отрицательным")
	}
	if c.RequestTimeout <= 0 {
		return errors.New("-request-timeout должен быть положительным")
	}
	if c.ShutdownTimeout <= 0 {
		return errors.New("-shutdown-timeout должен быть положительным")
	}
	return nil
}

// defaultBaseURL избавляет от ошибки "base-url без схемы" и от "base-url
func defaultBaseURL(httpAddr string) string {
	host, port, err := net.SplitHostPort(httpAddr)
	if err != nil {
		return "http://localhost"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// envReader читает переменные окружения, накапливая ошибки разбора
type envReader struct {
	lookup LookupEnv
	errs   []error
}

func (e *envReader) String(key, fallback string) string {
	if v, ok := e.lookup(key); ok && v != "" {
		return v
	}
	return fallback
}

func (e *envReader) Int(key string, fallback int) int {
	v, ok := e.lookup(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("переменная окружения %s=%q: ожидалось целое число", key, v))
		return fallback
	}
	return n
}

func (e *envReader) Duration(key string, fallback time.Duration) time.Duration {
	v, ok := e.lookup(key)
	if !ok || v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("переменная окружения %s=%q: ожидалась длительность вида 30s или 5m", key, v))
		return fallback
	}
	return d
}

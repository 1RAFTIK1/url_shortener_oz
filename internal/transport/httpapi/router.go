package httpapi

import (
	"log/slog"
	"net/http"
	"time"
)

// maxBodyBytes 8 КиБ
const maxBodyBytes = 8 << 10

// RouterConfig то, что роутеру нужно от конфигурации
type RouterConfig struct {
	BaseURL        string
	RequestTimeout time.Duration
}

// NewRouter собирает маршруты и цепочку middleware
func NewRouter(svc Shortener, pinger Pinger, log *slog.Logger, cfg RouterConfig) http.Handler {
	h := &handler{
		svc:     svc,
		pinger:  pinger,
		log:     log,
		baseURL: cfg.BaseURL,
	}

	// Маршрутизация из стандартной библиотеки
	// Коллизий между "/{code}" и служебными путями быть не может
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/links", h.createLink)
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /readyz", h.ready)
	mux.HandleFunc("GET /{code}", h.getLink)
	mux.HandleFunc("/", h.fallback)

	//  1. requestID идентификатор нужен всем, кто ниже
	//  2. logging   снаружи recover, поэтому увидит и статус 500 от паники
	//  3. recover   ловит панику раньше, чем она дойдёт до сервера
	//  4. timeout   ограничивает только полезную работу, не логирование
	//  5. maxBody   ближе всего к обработчику, ограничивает чтение тела
	return chain(mux,
		withRequestID,
		withLogging(log),
		withRecover(log),
		withTimeout(cfg.RequestTimeout),
		withMaxBody(maxBodyBytes),
	)
}

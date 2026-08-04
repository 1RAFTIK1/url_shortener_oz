package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

// Shortener — то, что транспорту нужно от предметной области
type Shortener interface {
	Shorten(ctx context.Context, rawURL string) (shortener.Link, bool, error)
	Resolve(ctx context.Context, code string) (shortener.Link, error)
}

// Pinger источник ответа для /readyz
type Pinger interface {
	Ping(ctx context.Context) error
}

type handler struct {
	svc     Shortener
	pinger  Pinger
	log     *slog.Logger
	baseURL string
}

// createLink обрабатывает POST /api/v1/links
// 201 ссылка создана этим запросом, 200 уже существовала
func (h *handler) createLink(w http.ResponseWriter, r *http.Request) {
	if !hasJSONContentType(r) {
		writeError(w, h.log, http.StatusUnsupportedMediaType, codeUnsupportedType)
		return
	}

	var req createLinkRequest
	dec := json.NewDecoder(r.Body)
	// Лишнее поле почти всегда опечатка клиента
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, h.log, http.StatusRequestEntityTooLarge, codePayloadTooLarge)
			return
		}
		// Пустое тело (io.EOF)
		if !errors.Is(err, io.EOF) {
			h.log.DebugContext(r.Context(), "не удалось разобрать тело запроса", "error", err)
		}
		writeError(w, h.log, http.StatusBadRequest, codeBadRequest)
		return
	}

	link, created, err := h.svc.Shorten(r.Context(), req.URL)
	if err != nil {
		writeDomainError(w, r, h.log, err)
		return
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
		// Location по стандарту сопровождает 201 короткая ссылка
		w.Header().Set("Location", h.shortURL(link.Code))
	}
	writeJSON(w, h.log, status, h.toResponse(link))
}

// getLink обрабатывает GET /{code}
func (h *handler) getLink(w http.ResponseWriter, r *http.Request) {
	// PathValue — маршрутизация из стандартной библиотеки, доступная с Go 1.22.
	link, err := h.svc.Resolve(r.Context(), r.PathValue("code"))
	if err != nil {
		writeDomainError(w, r, h.log, err)
		return
	}
	writeJSON(w, h.log, http.StatusOK, h.toResponse(link))
}

// health отвечает на «процесс жив»
func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, h.log, http.StatusOK, map[string]string{"status": "ok"})
}

// ready отвечает на «сервис может обслуживать запросы» и потому проверяет
// хранилище. Балансировщик по этому ответу выводит инстанс из ротации.
func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.pinger.Ping(r.Context()); err != nil {
		h.log.ErrorContext(r.Context(), "хранилище недоступно", "error", err)
		writeError(w, h.log, http.StatusServiceUnavailable, codeUnavailable)
		return
	}
	writeJSON(w, h.log, http.StatusOK, map[string]string{"status": "ok"})
}

// fallback обслуживает всё, что не подошло ни одному маршрутус
// раньше, чем ServeMux успевает решить, что не совпал только метод.
func (h *handler) fallback(w http.ResponseWriter, r *http.Request) {
	if allowed, ok := knownPaths[r.URL.Path]; ok {
		w.Header().Set("Allow", allowed)
		writeError(w, h.log, http.StatusMethodNotAllowed, codeMethodNotAllowed)
		return
	}
	writeError(w, h.log, http.StatusNotFound, codeNotFound)
}

var knownPaths = map[string]string{
	"/api/v1/links": http.MethodPost,
	"/healthz":      http.MethodGet,
	"/readyz":       http.MethodGet,
}

func (h *handler) toResponse(l shortener.Link) linkResponse {
	return linkResponse{
		Code:      l.Code,
		ShortURL:  h.shortURL(l.Code),
		URL:       l.URL,
		CreatedAt: l.CreatedAt,
	}
}

func (h *handler) shortURL(code string) string {
	return h.baseURL + "/" + code
}

// hasJSONContentType разбирает заголовок правильно: "application/json;
// charset=utf-8" — валидный JSON-тип, и простое сравнение строк его бы отвергло
func hasJSONContentType(r *http.Request) bool {
	raw := r.Header.Get("Content-Type")
	if raw == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(raw)
	return err == nil && mediaType == "application/json"
}

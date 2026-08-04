package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

// Машиночитаемые коды ошибок. Это часть публичного контракта API,
// поэтому менять их значения нельзя без версии API.
const (
	codeBadRequest       = "bad_request"
	codeInvalidURL       = "invalid_url"
	codeURLTooLong       = "url_too_long"
	codeInvalidCode      = "invalid_code"
	codeNotFound         = "not_found"
	codeMethodNotAllowed = "method_not_allowed"
	codeUnsupportedType  = "unsupported_media_type"
	codePayloadTooLarge  = "payload_too_large"
	codeStorageFull      = "storage_full"
	codeUnavailable      = "storage_unavailable"
	codeTimeout          = "timeout"
	codeInternal         = "internal_error"
)

// messages — единственное место, где живут человекочитаемые тексты ошибок
var messages = map[string]string{
	codeBadRequest:       "тело запроса должно быть объектом JSON с единственным полем url",
	codeInvalidURL:       "url должен быть абсолютным http(s)-адресом без учётных данных",
	codeURLTooLong:       "url не может быть длиннее 2048 символов",
	codeInvalidCode:      "код ссылки состоит ровно из 10 символов латиницы, цифр и подчёркивания",
	codeNotFound:         "ссылка не найдена",
	codeMethodNotAllowed: "метод не поддерживается для этого пути",
	codeUnsupportedType:  "ожидается Content-Type: application/json",
	codePayloadTooLarge:  "тело запроса слишком большое",
	codeStorageFull:      "хранилище переполнено, повторите позже",
	codeUnavailable:      "хранилище временно недоступно, повторите позже",
	codeTimeout:          "сервис не успел обработать запрос, повторите позже",
	codeInternal:         "внутренняя ошибка сервиса",
}

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	// Заголовки уже отправлены, исправить ответ нельзя
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Error("не удалось записать тело ответа", "error", err)
	}
}

func writeError(w http.ResponseWriter, log *slog.Logger, status int, code string) {
	writeJSON(w, log, status, errorResponse{
		Error: errorBody{Code: code, Message: messages[code]},
	})
}

// writeDomainError — единственная точка, где доменная ошибка превращается в
// HTTP-ответ
func writeDomainError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	status, code := classify(err)

	if status >= http.StatusInternalServerError {
		// 5xx — сервер полная ошибка нужна в логе с идентификатором запроса
		log.ErrorContext(r.Context(), "запрос завершился ошибкой",
			"error", err, "status", status,
			"request_id", requestIDFromContext(r.Context()))
	} else {
		// 4xx — на клиенте
		log.DebugContext(r.Context(), "запрос отклонён", "error", err, "status", status)
	}

	writeError(w, log, status, code)
}

// classify сопоставляет доменную ошибку со статусом
func classify(err error) (status int, code string) {
	switch {
	case errors.Is(err, shortener.ErrInvalidURL):
		return http.StatusBadRequest, codeInvalidURL
	case errors.Is(err, shortener.ErrURLTooLong):
		return http.StatusBadRequest, codeURLTooLong
	case errors.Is(err, shortener.ErrInvalidCode):
		return http.StatusBadRequest, codeInvalidCode
	case errors.Is(err, shortener.ErrNotFound):
		return http.StatusNotFound, codeNotFound
	case errors.Is(err, shortener.ErrStorageFull):
		// 503, а не 500: состояние временное, повторить имеет смысл
		return http.StatusServiceUnavailable, codeStorageFull
	case errors.Is(err, shortener.ErrStorageUnavailable):
		return http.StatusServiceUnavailable, codeUnavailable
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, codeTimeout
	default:
		// Сюда попадает и ErrNoFreeCode
		return http.StatusInternalServerError, codeInternal
	}
}

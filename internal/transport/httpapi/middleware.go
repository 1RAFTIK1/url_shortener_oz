package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// ctxKey — приватный тип ключа контекста
type ctxKey int

const requestIDKey ctxKey = iota

const requestIDHeader = "X-Request-Id"

type middleware func(http.Handler) http.Handler

// chain собирает цепочку так, что первый аргумент оказывается самым внешним, а последний  самым внутренним
func chain(h http.Handler, middlewares ...middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// withRequestID принимает идентификатор от клиента или выдаёт свой и всегда
// возвращает его в заголовке
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestIDHeader)
		if id == "" {
			id = newRequestID()
		}

		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(buf[:])
}

// withLogging пишет одну строку на запрос Запись отложена через defer,
// поэтому строка появится даже если обработчик запаниковал
func withLogging(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			defer func() {
				// LogAttrs вместо log.Info(...): не аллоцирует слайс any
				// на каждый запрос, что заметно на горячем пути
				log.LogAttrs(r.Context(), slog.LevelInfo, "обработан запрос",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", rec.status),
					slog.Int("bytes", rec.written),
					slog.Duration("duration", time.Since(started)),
					slog.String("request_id", requestIDFromContext(r.Context())),
				)
			}()

			next.ServeHTTP(rec, r)
		})
	}
}

// withRecover не даёт панике в одном запросе уронить весь процесс
func withRecover(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// http.ErrAbortHandler служебная паника самого net/http
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				log.LogAttrs(r.Context(), slog.LevelError, "паника в обработчике",
					slog.Any("panic", rec),
					slog.String("request_id", requestIDFromContext(r.Context())),
					slog.String("stack", string(debug.Stack())),
				)
				writeError(w, log, http.StatusInternalServerError, codeInternal)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// withTimeout ограничивает время обработки
func withTimeout(d time.Duration) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// withMaxBody не даёт одному клиенту заставить сервис читать гигабайт в память
func withMaxBody(limit int64) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder запоминает статус и объём ответа для лога
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.written += n
	return n, err
}

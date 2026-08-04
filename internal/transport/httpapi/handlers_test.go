package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
)

// fakeService возвращает заранее заданный результат
type fakeService struct {
	link    shortener.Link
	created bool
	err     error
}

func (f *fakeService) Shorten(context.Context, string) (shortener.Link, bool, error) {
	return f.link, f.created, f.err
}

func (f *fakeService) Resolve(context.Context, string) (shortener.Link, error) {
	return f.link, f.err
}

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

func testRouter(svc Shortener, pinger Pinger) http.Handler {
	return NewRouter(svc, pinger, slog.New(slog.DiscardHandler), RouterConfig{
		BaseURL:        "http://localhost:8081",
		RequestTimeout: time.Second,
	})
}

func do(t *testing.T, h http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()

	// t.Context() отменяется по завершении теста
	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) errorResponse {
	t.Helper()

	var resp errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("тело %q не разобралось как JSON-ошибка: %v", rec.Body.String(), err)
	}
	return resp
}

func TestCreateLinkCreated(t *testing.T) {
	link := shortener.Link{
		Code:      "aB3_x9Zq0k",
		URL:       "https://example.com/a",
		CreatedAt: time.Now().UTC(),
	}
	h := testRouter(&fakeService{link: link, created: true}, fakePinger{})

	rec := do(t, h, http.MethodPost, "/api/v1/links", "application/json", `{"url":"https://example.com/a"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, ожидался 201; тело: %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != "http://localhost:8081/aB3_x9Zq0k" {
		t.Errorf("Location = %q", got)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}

	var resp linkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("не разобрать тело: %v", err)
	}
	if resp.Code != link.Code {
		t.Errorf("code = %q", resp.Code)
	}
	if resp.ShortURL != "http://localhost:8081/aB3_x9Zq0k" {
		t.Errorf("short_url = %q — клиент не должен склеивать адрес сам", resp.ShortURL)
	}
	if resp.URL != link.URL {
		t.Errorf("url = %q", resp.URL)
	}
}

// Различие 201 и 200 это то, как клиент узнаёт, создал он ссылку или
// получил существующую, не делая второго запроса
func TestCreateLinkExistingReturns200(t *testing.T) {
	link := shortener.Link{Code: "aB3_x9Zq0k", URL: "https://example.com/a"}
	h := testRouter(&fakeService{link: link, created: false}, fakePinger{})

	rec := do(t, h, http.MethodPost, "/api/v1/links", "application/json", `{"url":"https://example.com/a"}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, ожидался 200 для уже существующей ссылки", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "" {
		t.Errorf("Location = %q, при 200 ничего не создано и заголовок не нужен", got)
	}
}

// Вся таблица ошибок из проектирования одним тестом
func TestCreateLinkErrors(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
		svcErr      error
		wantStatus  int
		wantCode    string
	}{
		{
			name: "без Content-Type", contentType: "", body: `{"url":"https://a.example"}`,
			wantStatus: http.StatusUnsupportedMediaType, wantCode: codeUnsupportedType,
		},
		{
			name: "чужой Content-Type", contentType: "text/plain", body: `{"url":"https://a.example"}`,
			wantStatus: http.StatusUnsupportedMediaType, wantCode: codeUnsupportedType,
		},
		{
			name: "Content-Type с charset принимается", contentType: "application/json; charset=utf-8",
			body: `{"url":"https://a.example"}`, wantStatus: http.StatusCreated,
		},
		{
			name: "пустое тело", contentType: "application/json", body: "",
			wantStatus: http.StatusBadRequest, wantCode: codeBadRequest,
		},
		{
			name: "битый JSON", contentType: "application/json", body: `{"url":`,
			wantStatus: http.StatusBadRequest, wantCode: codeBadRequest,
		},
		{
			name: "лишнее поле", contentType: "application/json", body: `{"url":"https://a.example","ttl":10}`,
			wantStatus: http.StatusBadRequest, wantCode: codeBadRequest,
		},
		{
			name: "тело больше лимита", contentType: "application/json",
			body:       fmt.Sprintf(`{"url":"https://a.example/%s"}`, strings.Repeat("a", maxBodyBytes)),
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: codePayloadTooLarge,
		},
		{
			name: "домен отверг URL", contentType: "application/json", body: `{"url":"ftp://a.example"}`,
			svcErr:     fmt.Errorf("%w: схема", shortener.ErrInvalidURL),
			wantStatus: http.StatusBadRequest, wantCode: codeInvalidURL,
		},
		{
			name: "URL слишком длинный", contentType: "application/json", body: `{"url":"https://a.example"}`,
			svcErr:     shortener.ErrURLTooLong,
			wantStatus: http.StatusBadRequest, wantCode: codeURLTooLong,
		},
		{
			name: "хранилище недоступно", contentType: "application/json", body: `{"url":"https://a.example"}`,
			svcErr:     fmt.Errorf("save link: %w", shortener.ErrStorageUnavailable),
			wantStatus: http.StatusServiceUnavailable, wantCode: codeUnavailable,
		},
		{
			name: "память переполнена", contentType: "application/json", body: `{"url":"https://a.example"}`,
			svcErr:     shortener.ErrStorageFull,
			wantStatus: http.StatusServiceUnavailable, wantCode: codeStorageFull,
		},
		{
			name: "не удалось подобрать код", contentType: "application/json", body: `{"url":"https://a.example"}`,
			svcErr:     shortener.ErrNoFreeCode,
			wantStatus: http.StatusInternalServerError, wantCode: codeInternal,
		},
		{
			name: "истёк таймаут запроса", contentType: "application/json", body: `{"url":"https://a.example"}`,
			svcErr:     fmt.Errorf("save link: %w", context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout, wantCode: codeTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{
				link:    shortener.Link{Code: "aB3_x9Zq0k", URL: "https://a.example/"},
				created: true,
				err:     tt.svcErr,
			}
			h := testRouter(svc, fakePinger{})

			rec := do(t, h, http.MethodPost, "/api/v1/links", tt.contentType, tt.body)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, ожидался %d; тело: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantCode == "" {
				return
			}

			resp := decodeError(t, rec)
			if resp.Error.Code != tt.wantCode {
				t.Errorf("error.code = %q, ожидался %q", resp.Error.Code, tt.wantCode)
			}
			if resp.Error.Message == "" {
				t.Error("error.message пустой — клиенту нечего показать пользователю")
			}
		})
	}
}

// Отдельный тест ровно на то, что внутренности не протекают наружу
func TestInternalErrorDoesNotLeakDetails(t *testing.T) {
	secret := `pq: relation "links" does not exist in schema public`
	svc := &fakeService{err: errors.New(secret)}
	h := testRouter(svc, fakePinger{})

	rec := do(t, h, http.MethodPost, "/api/v1/links", "application/json", `{"url":"https://a.example"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, ожидался 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "links") {
		t.Errorf("тело %q содержит внутренние детали ошибки", rec.Body.String())
	}
}

func TestGetLink(t *testing.T) {
	link := shortener.Link{Code: "aB3_x9Zq0k", URL: "https://example.com/a"}

	t.Run("успешный ответ", func(t *testing.T) {
		h := testRouter(&fakeService{link: link}, fakePinger{})

		rec := do(t, h, http.MethodGet, "/aB3_x9Zq0k", "", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; тело: %s", rec.Code, rec.Body)
		}

		var resp linkResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("не разобрать тело: %v", err)
		}
		if resp.URL != link.URL {
			t.Errorf("url = %q, ожидался %q", resp.URL, link.URL)
		}
	})

	t.Run("код не найден", func(t *testing.T) {
		h := testRouter(&fakeService{err: shortener.ErrNotFound}, fakePinger{})

		rec := do(t, h, http.MethodGet, "/aB3_x9Zq0k", "", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, ожидался 404", rec.Code)
		}
		if got := decodeError(t, rec).Error.Code; got != codeNotFound {
			t.Errorf("error.code = %q", got)
		}
	})

	t.Run("некорректный код", func(t *testing.T) {
		h := testRouter(&fakeService{err: shortener.ErrInvalidCode}, fakePinger{})

		rec := do(t, h, http.MethodGet, "/short", "", "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, ожидался 400", rec.Code)
		}
	})
}

// Маршрутизация
func TestRouting(t *testing.T) {
	h := testRouter(&fakeService{link: shortener.Link{Code: "aB3_x9Zq0k"}}, fakePinger{})

	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
		wantAllow  string
	}{
		{"healthz", http.MethodGet, "/healthz", http.StatusOK, ""},
		{"readyz", http.MethodGet, "/readyz", http.StatusOK, ""},
		{"GET на путь создания", http.MethodGet, "/api/v1/links", http.StatusMethodNotAllowed, http.MethodPost},
		{"DELETE на путь создания", http.MethodDelete, "/api/v1/links", http.StatusMethodNotAllowed, http.MethodPost},
		{"POST на healthz", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, http.MethodGet},
		{"неизвестный вложенный путь", http.MethodGet, "/a/b/c", http.StatusNotFound, ""},
		{"корень", http.MethodGet, "/", http.StatusNotFound, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.target, "", "")

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, ожидался %d; тело: %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantAllow != "" && rec.Header().Get("Allow") != tt.wantAllow {
				t.Errorf("Allow = %q, ожидался %q", rec.Header().Get("Allow"), tt.wantAllow)
			}
			if tt.wantStatus != http.StatusOK {
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
					t.Errorf("Content-Type = %q — ошибки тоже должны быть JSON", ct)
				}
			}
		})
	}
}

func TestReadyzReportsBrokenStorage(t *testing.T) {
	h := testRouter(&fakeService{}, fakePinger{err: errors.New("connection refused")})

	rec := do(t, h, http.MethodGet, "/readyz", "", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, ожидался 503", rec.Code)
	}
}

func TestRequestIDIsAssignedAndEchoed(t *testing.T) {
	h := testRouter(&fakeService{}, fakePinger{})

	rec := do(t, h, http.MethodGet, "/healthz", "", "")
	if rec.Header().Get(requestIDHeader) == "" {
		t.Error("сервис должен проставлять X-Request-Id, если клиент его не прислал")
	}

	// Сквозной идентификатор от клиента должен сохраняться иначе трассировка
	// через несколько сервисов разваливается
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil)
	req.Header.Set(requestIDHeader, "trace-from-client")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get(requestIDHeader); got != "trace-from-client" {
		t.Errorf("X-Request-Id = %q, идентификатор клиента должен сохраняться сквозным", got)
	}
}

// Паника в одном запросе не должна ронять процесс
func TestPanicIsRecovered(t *testing.T) {
	h := testRouter(panicService{}, fakePinger{})

	rec := do(t, h, http.MethodGet, "/aB3_x9Zq0k", "", "")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, ожидался 500", rec.Code)
	}
	if got := decodeError(t, rec).Error.Code; got != codeInternal {
		t.Errorf("error.code = %q", got)
	}
}

type panicService struct{}

func (panicService) Shorten(context.Context, string) (shortener.Link, bool, error) {
	panic("что-то пошло не так")
}

func (panicService) Resolve(context.Context, string) (shortener.Link, error) {
	panic("что-то пошло не так")
}

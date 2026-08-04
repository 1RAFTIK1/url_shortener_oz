// Пакет httpapi_test — внешний: тесты видят только то же, что и любой клиент.
package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/1RAFTIK1/url-shortener-oz/internal/codegen"
	"github.com/1RAFTIK1/url-shortener-oz/internal/shortener"
	"github.com/1RAFTIK1/url-shortener-oz/internal/storage/memory"
	"github.com/1RAFTIK1/url-shortener-oz/internal/transport/httpapi"
)

type linkBody struct {
	Code     string `json:"code"`
	ShortURL string `json:"short_url"`
	URL      string `json:"url"`
}

// newServer поднимает настоящий сервер с настоящим сервисом, настоящим
// генератором и настоящим хранилищем
func newServer(t *testing.T) *httptest.Server {
	t.Helper()

	store := memory.New()
	t.Cleanup(func() { _ = store.Close() })

	log := slog.New(slog.DiscardHandler)
	svc := shortener.NewService(store, codegen.New(), log)

	// NewUnstartedServer нужен, чтобы узнать адрес до старта
	srv := httptest.NewUnstartedServer(nil)
	srv.Config.Handler = httpapi.NewRouter(svc, store, log, httpapi.RouterConfig{
		BaseURL:        "http://" + srv.Listener.Addr().String(),
		RequestTimeout: 2 * time.Second,
	})
	srv.Start()
	t.Cleanup(srv.Close)

	return srv
}

func doRequest(ctx context.Context, srv *httptest.Server, method, target, body string) (*http.Response, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return srv.Client().Do(req)
}

func postLink(t *testing.T, srv *httptest.Server, url string) (int, linkBody) {
	t.Helper()

	// Тело собирается через json.Marshal, а не конкатенацией
	payload, err := json.Marshal(map[string]string{"url": url})
	if err != nil {
		t.Fatalf("подготовка тела: %v", err)
	}

	resp, err := doRequest(t.Context(), srv, http.MethodPost, srv.URL+"/api/v1/links", string(payload))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var parsed linkBody
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < http.StatusBadRequest {
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("разбор тела %q: %v", raw, err)
		}
	}
	return resp.StatusCode, parsed
}

// Полный жизненный цикл ссылки через HTTP маршрутизация,
// middleware, статусы и JSON проверяются вместе
func TestLinkLifecycle(t *testing.T) {
	srv := newServer(t)
	const target = "https://ozon.ru/category/smartfony"

	status, created := postLink(t, srv, target)
	if status != http.StatusCreated {
		t.Fatalf("первый POST: status = %d, ожидался 201", status)
	}
	if err := shortener.ValidateCode(created.Code); err != nil {
		t.Fatalf("сервис вернул невалидный код %q: %v", created.Code, err)
	}
	if created.ShortURL != srv.URL+"/"+created.Code {
		t.Errorf("short_url = %q, ожидался %q", created.ShortURL, srv.URL+"/"+created.Code)
	}

	status, repeated := postLink(t, srv, target)
	if status != http.StatusOK {
		t.Errorf("повторный POST: status = %d, ожидался 200", status)
	}
	if repeated.Code != created.Code {
		t.Errorf("повторный POST вернул другой код: %q против %q", repeated.Code, created.Code)
	}

	// Тот же адрес в другой записи должен схлопнуться в ту же ссылку
	status, normalized := postLink(t, srv, "HTTPS://OZON.ru:443/category/smartfony")
	if status != http.StatusOK || normalized.Code != created.Code {
		t.Errorf("нормализованный адрес дал status=%d code=%q, ожидались 200 и %q",
			status, normalized.Code, created.Code)
	}

	// Забираем ссылку по тому самому short_url, который сервис сам и выдал
	resp, err := doRequest(t.Context(), srv, http.MethodGet, created.ShortURL, "")
	if err != nil {
		t.Fatalf("GET по короткой ссылке: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET: status = %d, ожидался 200", resp.StatusCode)
	}

	var resolved linkBody
	if err := json.NewDecoder(resp.Body).Decode(&resolved); err != nil {
		t.Fatalf("разбор ответа GET: %v", err)
	}
	if resolved.URL != target {
		t.Errorf("url = %q, ожидался исходный %q", resolved.URL, target)
	}
}

func TestUnknownCodeReturns404(t *testing.T) {
	srv := newServer(t)

	resp, err := doRequest(t.Context(), srv, http.MethodGet, srv.URL+"/zzzzzzzzzz", "")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, ожидался 404", resp.StatusCode)
	}
}

func TestInvalidURLReturns400(t *testing.T) {
	srv := newServer(t)

	for _, target := range []string{"ftp://example.com", "example.com", "", "javascript:alert(1)"} {
		if status, _ := postLink(t, srv, target); status != http.StatusBadRequest {
			t.Errorf("POST %q: status = %d, ожидался 400", target, status)
		}
	}
}

// Сотня одновременных запросов одного адреса обязана дать ровно одну ссылку
func TestConcurrentPostsOfSameURLReturnSameCode(t *testing.T) {
	srv := newServer(t)
	const (
		target  = "https://ozon.ru/concurrent"
		clients = 100
	)

	ctx := t.Context()
	payload, err := json.Marshal(map[string]string{"url": target})
	if err != nil {
		t.Fatalf("подготовка тела: %v", err)
	}

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		codes = make(map[string]int)
	)

	start := make(chan struct{})
	for range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			resp, err := doRequest(ctx, srv, http.MethodPost, srv.URL+"/api/v1/links", string(payload))
			if err != nil {
				t.Errorf("POST: %v", err)
				return
			}
			defer func() { _ = resp.Body.Close() }()

			var body linkBody
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Errorf("разбор тела: %v", err)
				return
			}

			mu.Lock()
			codes[body.Code]++
			mu.Unlock()
		}()
	}

	close(start)
	wg.Wait()

	if len(codes) != 1 {
		t.Errorf("получено %d различных кодов на один URL, ожидался ровно один: %v", len(codes), codes)
	}
}

package shortener

import (
	"context"
	"errors"
	"log/slog"
	"testing"
)

type fakeRepo struct {
	byCode map[string]Link
	byURL  map[string]Link

	saveErr   error // если задана, Save всегда возвращает её
	saveCalls int
	getCalls  int
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		byCode: make(map[string]Link),
		byURL:  make(map[string]Link),
	}
}

func (f *fakeRepo) put(l Link) {
	f.byCode[l.Code] = l
	f.byURL[l.URL] = l
}

func (f *fakeRepo) Save(_ context.Context, l Link) (Link, bool, error) {
	f.saveCalls++
	if f.saveErr != nil {
		return Link{}, false, f.saveErr
	}
	if existing, ok := f.byURL[l.URL]; ok {
		return existing, false, nil
	}
	if _, ok := f.byCode[l.Code]; ok {
		return Link{}, false, ErrCodeTaken
	}
	f.put(l)
	return l, true, nil
}

func (f *fakeRepo) GetByCode(_ context.Context, code string) (Link, error) {
	f.getCalls++
	l, ok := f.byCode[code]
	if !ok {
		return Link{}, ErrNotFound
	}
	return l, nil
}

func seqGenerator(codes ...string) Generator {
	i := 0
	return GeneratorFunc(func() (string, error) {
		if i >= len(codes) {
			return "", errors.New("тестовый генератор исчерпан")
		}
		c := codes[i]
		i++
		return c, nil
	})
}

func newTestService(repo Repository, gen Generator) *Service {
	return NewService(repo, gen, slog.New(slog.DiscardHandler))
}

func TestShortenCreatesLink(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, seqGenerator("aaaaaaaaaa"))

	link, created, err := svc.Shorten(context.Background(), "HTTPS://Example.com")
	if err != nil {
		t.Fatalf("Shorten вернул ошибку: %v", err)
	}
	if !created {
		t.Error("created = false, ожидалось true для нового URL")
	}
	if link.Code != "aaaaaaaaaa" {
		t.Errorf("Code = %q, ожидалось %q", link.Code, "aaaaaaaaaa")
	}
	if want := "https://example.com/"; link.URL != want {
		t.Errorf("URL = %q, ожидался нормализованный %q", link.URL, want)
	}
	if link.CreatedAt.IsZero() {
		t.Error("CreatedAt не заполнен")
	}
}

func TestShortenDeduplicates(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, seqGenerator("aaaaaaaaaa", "bbbbbbbbbb"))
	ctx := context.Background()

	first, created, err := svc.Shorten(ctx, "https://example.com/a")
	if err != nil || !created {
		t.Fatalf("первый вызов: created=%v, err=%v", created, err)
	}

	second, created, err := svc.Shorten(ctx, "HTTPS://EXAMPLE.com:443/a")
	if err != nil {
		t.Fatalf("второй вызов вернул ошибку: %v", err)
	}
	if created {
		t.Error("created = true, ожидалось false: ссылка уже существовала")
	}
	if second.Code != first.Code {
		t.Errorf("код изменился: было %q, стало %q", first.Code, second.Code)
	}
	if repo.saveCalls != 2 {
		t.Errorf("saveCalls = %d, ожидалось 2", repo.saveCalls)
	}
}

func TestShortenRetriesOnCodeCollision(t *testing.T) {
	repo := newFakeRepo()
	repo.put(Link{Code: "aaaaaaaaaa", URL: "https://taken.example/"})
	svc := newTestService(repo, seqGenerator("aaaaaaaaaa", "bbbbbbbbbb"))

	link, created, err := svc.Shorten(context.Background(), "https://new.example/")
	if err != nil {
		t.Fatalf("Shorten вернул ошибку: %v", err)
	}
	if !created {
		t.Error("created = false, ожидалось true")
	}
	if link.Code != "bbbbbbbbbb" {
		t.Errorf("Code = %q, ожидался второй код %q", link.Code, "bbbbbbbbbb")
	}
	if repo.saveCalls != 2 {
		t.Errorf("saveCalls = %d, ожидалось 2 (первая попытка + повтор)", repo.saveCalls)
	}
}

func TestShortenGivesUpAfterMaxAttempts(t *testing.T) {
	repo := newFakeRepo()
	repo.put(Link{Code: "aaaaaaaaaa", URL: "https://taken.example/"})
	svc := newTestService(repo, seqGenerator("aaaaaaaaaa", "aaaaaaaaaa", "aaaaaaaaaa"))

	_, _, err := svc.Shorten(context.Background(), "https://new.example/")
	if !errors.Is(err, ErrNoFreeCode) {
		t.Errorf("err = %v, ожидалась ErrNoFreeCode", err)
	}
	if !errors.Is(err, ErrCodeTaken) {
		t.Errorf("err = %v, ожидалось, что ErrCodeTaken сохранится в цепочке", err)
	}
	if repo.saveCalls != svc.maxAttempts {
		t.Errorf("saveCalls = %d, ожидалось ровно maxAttempts = %d", repo.saveCalls, svc.maxAttempts)
	}
}

func TestShortenDoesNotRetryOnStorageError(t *testing.T) {
	repo := newFakeRepo()
	repo.saveErr = ErrStorageUnavailable
	svc := newTestService(repo, seqGenerator("aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc"))

	_, _, err := svc.Shorten(context.Background(), "https://example.com/a")
	if !errors.Is(err, ErrStorageUnavailable) {
		t.Errorf("err = %v, ожидалась ErrStorageUnavailable", err)
	}
	if repo.saveCalls != 1 {
		t.Errorf("saveCalls = %d, ожидался ровно 1 вызов без повторов", repo.saveCalls)
	}
}

func TestShortenRejectsBadURLBeforeStorage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"без схемы", "example.com", ErrInvalidURL},
		{"схема ftp", "ftp://example.com", ErrInvalidURL},
		{"пустая строка", "", ErrInvalidURL},
		{"слишком длинный", "https://e.com/" + string(make([]byte, MaxURLLength)), ErrURLTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeRepo()
			svc := newTestService(repo, seqGenerator("aaaaaaaaaa"))

			_, _, err := svc.Shorten(context.Background(), tt.raw)
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, ожидалась %v", err, tt.want)
			}
			if repo.saveCalls != 0 {
				t.Errorf("saveCalls = %d, хранилище не должно было вызываться", repo.saveCalls)
			}
		})
	}
}

func TestShortenPropagatesGeneratorError(t *testing.T) {
	wantErr := errors.New("источник случайности недоступен")
	repo := newFakeRepo()
	svc := newTestService(repo, GeneratorFunc(func() (string, error) {
		return "", wantErr
	}))

	_, _, err := svc.Shorten(context.Background(), "https://example.com/a")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, ожидалась исходная ошибка генератора", err)
	}
	if repo.saveCalls != 0 {
		t.Errorf("saveCalls = %d, ожидался 0", repo.saveCalls)
	}
}

func TestResolve(t *testing.T) {
	stored := Link{Code: "aaaaaaaaaa", URL: "https://example.com/a"}

	t.Run("существующий код", func(t *testing.T) {
		repo := newFakeRepo()
		repo.put(stored)
		svc := newTestService(repo, seqGenerator())

		got, err := svc.Resolve(context.Background(), stored.Code)
		if err != nil {
			t.Fatalf("Resolve вернул ошибку: %v", err)
		}
		if got.URL != stored.URL {
			t.Errorf("URL = %q, ожидался %q", got.URL, stored.URL)
		}
	})

	t.Run("несуществующий код", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newTestService(repo, seqGenerator())

		_, err := svc.Resolve(context.Background(), "zzzzzzzzzz")
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, ожидалась ErrNotFound", err)
		}
	})

	t.Run("некорректный код не доходит до хранилища", func(t *testing.T) {
		repo := newFakeRepo()
		svc := newTestService(repo, seqGenerator())

		_, err := svc.Resolve(context.Background(), "слишком-длинный-код")
		if !errors.Is(err, ErrInvalidCode) {
			t.Errorf("err = %v, ожидалась ErrInvalidCode", err)
		}
		if repo.getCalls != 0 {
			t.Errorf("getCalls = %d, хранилище не должно было вызываться", repo.getCalls)
		}
	})
}

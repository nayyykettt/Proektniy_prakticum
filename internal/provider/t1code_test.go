package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeT1 эмулирует GitLab-совместимый API Т1 Код.
func fakeT1(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits429 atomic.Int32
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("PRIVATE-TOKEN") != "secret" {
				http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET /api/v4/projects/group%2Fapp/merge_requests", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("updated_after") == "" {
			t.Error("ожидался updated_after для инкрементальной загрузки")
		}
		// Две страницы: пагинация по X-Next-Page.
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("X-Next-Page", "2")
			fmt.Fprint(w, `[{"iid":1,"title":"Parser","state":"merged","created_at":"2026-03-02T09:00:00Z",
				"merged_at":"2026-03-03T09:00:00Z","author":{"username":"ivan","name":"Иван"}}]`)
			return
		}
		fmt.Fprint(w, `[{"iid":2,"title":"WIP","state":"opened","created_at":"2026-03-04T09:00:00Z",
			"author":{"username":"anna","name":"Анна"}}]`)
	}))
	mux.HandleFunc("GET /api/v4/projects/group%2Fapp/merge_requests/1/notes", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[
			{"body":"added 1 commit","system":true,"created_at":"2026-03-02T09:30:00Z","author":{"username":"ivan"}},
			{"body":"LGTM?","system":false,"created_at":"2026-03-02T09:40:00Z","author":{"username":"ivan"}},
			{"body":"Поправь имя","system":false,"created_at":"2026-03-02T11:00:00Z","author":{"username":"anna","name":"Анна"}},
			{"body":"ещё","system":false,"created_at":"2026-03-02T12:00:00Z","author":{"username":"anna","name":"Анна"}},
			{"body":"approved this merge request","system":true,"created_at":"2026-03-02T13:00:00Z","author":{"username":"petr","name":"Пётр"}}]`)
	}))
	mux.HandleFunc("GET /api/v4/projects/group%2Fapp/merge_requests/1/diffs", auth(func(w http.ResponseWriter, r *http.Request) {
		// Первый запрос — 429, клиент должен повторить.
		if hits429.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `[{"diff":"--- a/a.go\n+++ b/a.go\n@@ -1,2 +1,3 @@\n-old\n+new\n+more\n ctx\n"}]`)
	}))
	mux.HandleFunc("GET /api/v4/projects/group%2Fapp/merge_requests/2/notes", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	}))
	mux.HandleFunc("GET /api/v4/projects/group%2Fapp/issues", auth(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"iid":7,"title":"Bug","labels":["bug","backend"],"state":"closed","created_at":"2026-03-01T00:00:00Z",
			"closed_at":"2026-03-02T00:00:00Z","author":{"username":"lead"},"assignees":[{"username":"ivan","name":"Иван"}]}]`)
	}))
	mux.HandleFunc("GET /api/v4/users", auth(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("username") == "ivan" {
			fmt.Fprint(w, `[{"id":42}]`)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	mux.HandleFunc("PUT /api/v4/projects/group%2Fapp/issues/7", auth(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), `"assignee_ids":[42]`) {
			t.Errorf("тело запроса назначения: %s", b)
		}
		fmt.Fprint(w, `{}`)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits429
}

func newClient(t *testing.T, url, token string) *T1Code {
	c, err := NewT1Code(url+"/", "group/app", token, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.backoff = time.Millisecond
	return c
}

func TestFetchMergeRequests(t *testing.T) {
	srv, hits := fakeT1(t)
	c := newClient(t, srv.URL, "secret")
	mrs, err := c.FetchMergeRequests(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(mrs) != 2 {
		t.Fatalf("ожидалось 2 MR с двух страниц, got %d", len(mrs))
	}
	mr := mrs[0]
	if mr.Additions != 2 || mr.Deletions != 1 {
		t.Errorf("размер diff: +%d -%d", mr.Additions, mr.Deletions)
	}
	if hits.Load() != 2 {
		t.Errorf("ожидался повтор после 429, запросов: %d", hits.Load())
	}
	// Ревьюеры: Анна (2 комментария) и Пётр (approve); автор и системные заметки не учитываются.
	if len(mr.Reviews) != 2 || mr.Reviews[0].AuthorLogin != "anna" || mr.Reviews[0].Comments != 2 || mr.Comments != 2 {
		t.Fatalf("ревью: %+v comments=%d", mr.Reviews, mr.Comments)
	}
	if mr.FirstReviewAt == nil || mr.FirstReviewAt.Hour() != 11 {
		t.Errorf("первое ревью: %v", mr.FirstReviewAt)
	}
	if mrs[1].Additions != -1 {
		t.Error("для открытого MR размер не запрашивается")
	}
}

func TestFetchIssuesAndAssign(t *testing.T) {
	srv, _ := fakeT1(t)
	c := newClient(t, srv.URL, "secret")
	issues, err := c.FetchIssues(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].AssigneeLogin != "ivan" || len(issues[0].Labels) != 2 || issues[0].ClosedAt == nil {
		t.Fatalf("issues: %+v", issues)
	}
	if err := c.AssignIssue(context.Background(), 7, "ivan"); err != nil {
		t.Fatal(err)
	}
	if err := c.AssignIssue(context.Background(), 7, "ghost"); err == nil {
		t.Fatal("ожидалась ошибка для неизвестного пользователя")
	}
}

func TestUnauthorized(t *testing.T) {
	srv, _ := fakeT1(t)
	c := newClient(t, srv.URL, "wrong")
	_, err := c.FetchIssues(context.Background(), time.Time{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("ожидалась APIError 401, got %v", err)
	}
	if !strings.Contains(err.Error(), "токен") {
		t.Errorf("сообщение должно быть понятным: %v", err)
	}
}

func TestServerDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newClient(t, srv.URL, "x")
	if _, err := c.FetchIssues(context.Background(), time.Time{}); err == nil {
		t.Fatal("ожидалась ошибка при 502")
	}
	if _, err := NewT1Code("", "1", "x", nil); err == nil {
		t.Fatal("пустой адрес должен отклоняться")
	}
}

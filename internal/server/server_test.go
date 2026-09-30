package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"teampulse/internal/app"
	"teampulse/internal/config"
	"teampulse/internal/secure"
	"teampulse/internal/storage"
	"teampulse/internal/testutil"
	"teampulse/internal/zerocracy"
)

type env struct {
	srv  *httptest.Server
	app  *app.App
	repo string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{ReposDir: filepath.Join(dir, "repos"), AllowLocalRepos: true, MaxCommits: 1000,
		CloneTimeout: time.Minute, WorkdayStart: 9, WorkdayEnd: 19, APIToken: "api-token"}
	store, err := storage.Open(context.Background(), filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, _ := secure.NewBox([]byte("server-test-secret-123"))
	a := app.New(cfg, store, box, zerocracy.DefaultPolicy(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	h, err := New(a, NewAuth(a))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(func() { srv.Close(); a.Wait() })

	r := testutil.NewRepo(t)
	now := time.Now()
	for i := range 5 {
		r.Commit(testutil.FakeCommit{Author: "Иван", Email: "ivan@corp.ru", When: now.Add(-time.Duration(i+1) * 24 * time.Hour),
			Message: fmt.Sprintf("feat: step %d #%d", i, i), Files: map[string]string{"main.go": testutil.Lines(5+i, "x"), "main_test.go": testutil.Lines(i+1, "t")}})
	}
	return &env{srv: srv, app: a, repo: r.Dir}
}

func (e *env) api(t *testing.T, method, path, body string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer api-token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

func TestAPIFlow(t *testing.T) {
	e := newEnv(t)
	resp, body := e.api(t, "POST", "/api/projects", fmt.Sprintf(`{"name":"demo","repo_url":%q,"token":"secret-token"}`, e.repo))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(body, "secret-token") || strings.Contains(body, "token_enc") {
		t.Fatal("токен не должен возвращаться через API")
	}
	var p struct{ ID int64 }
	_ = json.Unmarshal([]byte(body), &p)

	resp, body = e.api(t, "POST", fmt.Sprintf("/api/projects/%d/sync?wait=true", p.ID), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"new_commits":5`) {
		t.Fatalf("sync: %d %s", resp.StatusCode, body)
	}
	resp, body = e.api(t, "GET", fmt.Sprintf("/api/projects/%d/metrics", p.ID), "")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"commits":5`) {
		t.Fatalf("metrics: %d %s", resp.StatusCode, body)
	}
	for _, path := range []string{"trends", "recommendations", "contribution", "assignments", "authors", "teams", "reports"} {
		if resp, body := e.api(t, "GET", fmt.Sprintf("/api/projects/%d/%s", p.ID, path), ""); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: %d %s", path, resp.StatusCode, body)
		}
	}
	resp, body = e.api(t, "POST", fmt.Sprintf("/api/projects/%d/reports", p.ID), `{"from":"2026-01-01","to":"2027-01-01"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("report: %d %s", resp.StatusCode, body)
	}
	var rep struct{ ID int64 }
	_ = json.Unmarshal([]byte(body), &rep)
	for _, f := range []string{"md", "csv", "json", "html"} {
		resp, body := e.api(t, "GET", fmt.Sprintf("/api/reports/%d?format=%s", rep.ID, f), "")
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "demo") {
			t.Errorf("report %s: %d", f, resp.StatusCode)
		}
	}

	// Ошибки ввода → понятные коды.
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/projects/999", "", 404},
		{"GET", "/api/projects/abc", "", 400},
		{"GET", fmt.Sprintf("/api/projects/%d/metrics?from=2026-05-01&to=2026-01-01", p.ID), "", 400},
		{"GET", fmt.Sprintf("/api/projects/%d/metrics?from=01.01.2026", p.ID), "", 400},
		{"POST", "/api/projects", `{"name":"x","repo_url":"ftp://bad"}`, 400},
		{"POST", "/api/projects", `{"name":`, 400},
		{"POST", "/api/projects", `{"unknown":1}`, 400},
		{"POST", "/api/projects", fmt.Sprintf(`{"name":"demo","repo_url":%q}`, e.repo), 409},
		{"GET", fmt.Sprintf("/api/reports/%d?format=pdf", rep.ID), "", 400},
	}
	for _, c := range cases {
		resp, body := e.api(t, c.method, c.path, c.body)
		if resp.StatusCode != c.status || !strings.Contains(body, "error") {
			t.Errorf("%s %s: got %d %s, want %d", c.method, c.path, resp.StatusCode, body, c.status)
		}
	}
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// user — браузер пользователя с собственными cookie.
type user struct {
	t    *testing.T
	e    *env
	c    *http.Client
	csrf string
}

func (e *env) newUser(t *testing.T) *user {
	jar, _ := cookiejar.New(nil)
	return &user{t: t, e: e, c: &http.Client{Jar: jar}}
}

func (u *user) post(path string, form url.Values) (*http.Response, string) {
	u.t.Helper()
	if u.csrf != "" && form.Get("csrf") == "" {
		form.Set("csrf", u.csrf)
	}
	resp, err := u.c.PostForm(u.e.srv.URL+path, form)
	if err != nil {
		u.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if m := csrfRe.FindSubmatch(b); m != nil {
		u.csrf = string(m[1])
	}
	return resp, string(b)
}

func (u *user) get(path string) (*http.Response, string) {
	u.t.Helper()
	resp, err := u.c.Get(u.e.srv.URL + path)
	if err != nil {
		u.t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if m := csrfRe.FindSubmatch(b); m != nil {
		u.csrf = string(m[1])
	}
	return resp, string(b)
}

// signup регистрирует пользователя и входит под ним.
func (e *env) signup(t *testing.T, login, password string) *user {
	t.Helper()
	u := e.newUser(t)
	resp, body := u.post("/register", url.Values{"login": {login}, "password": {password}, "password_confirm": {password}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Аккаунт создан") {
		t.Fatalf("регистрация %s: %d %s", login, resp.StatusCode, resp.Request.URL)
	}
	resp, _ = u.post("/login", url.Values{"login": {login}, "password": {password}, "next": {"/"}})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/" || u.csrf == "" {
		t.Fatalf("вход %s: %d %s", login, resp.StatusCode, resp.Request.URL)
	}
	return u
}

func TestRegistrationAndLogin(t *testing.T) {
	e := newEnv(t)
	resp, _ := http.Get(e.srv.URL + "/api/projects")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("API без токена: %d", resp.StatusCode)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, _ = noRedirect.Get(e.srv.URL + "/")
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/login") {
		t.Fatalf("страница без входа должна вести на /login: %d", resp.StatusCode)
	}
	if resp.Header.Get("Content-Security-Policy") == "" || resp.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("нет заголовков безопасности")
	}

	u := e.newUser(t)
	for path, want := range map[string][]string{
		"/login":    {"С возвращением", `href="/register"`, `name="login"`},
		"/register": {"Новый аккаунт", `name="password_confirm"`, "Повторите пароль", `href="/login"`},
	} {
		resp, body := u.get(path)
		for _, w := range want {
			if resp.StatusCode != http.StatusOK || !strings.Contains(body, w) {
				t.Errorf("%s: %d, нет %q", path, resp.StatusCode, w)
			}
		}
	}

	// Ошибки регистрации.
	bad := []struct {
		login, pass, confirm string
		status               int
		msg                  string
	}{
		{"alice", "password-1", "password-2", 400, "пароли не совпадают"},
		{"alice", "short", "short", 400, "пароль — от 8"},
		{"al", "password-1", "password-1", 400, "логин — от 3"},
		{"алиса", "password-1", "password-1", 400, "только латинские"},
		{"alice", "alice", "alice", 400, "пароль"},
	}
	for _, c := range bad {
		resp, body := u.post("/register", url.Values{"login": {c.login}, "password": {c.pass}, "password_confirm": {c.confirm}})
		if resp.StatusCode != c.status || !strings.Contains(body, c.msg) {
			t.Errorf("регистрация %+v: %d, нет %q", c, resp.StatusCode, c.msg)
		}
	}

	// Успешная регистрация → страница входа с подставленным логином.
	resp, body := u.post("/register", url.Values{"login": {"alice"}, "password": {"password-1"}, "password_confirm": {"password-1"}})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/login" || !strings.Contains(body, `value="alice"`) {
		t.Fatalf("после регистрации ожидалась страница входа: %d %s", resp.StatusCode, resp.Request.URL)
	}
	if resp, _ := u.get("/"); resp.Request.URL.Path != "/login" {
		t.Fatal("регистрация не должна сама выполнять вход")
	}
	resp, body = u.post("/register", url.Values{"login": {"ALICE"}, "password": {"password-1"}, "password_confirm": {"password-1"}})
	if resp.StatusCode != http.StatusConflict || !strings.Contains(body, "уже занят") {
		t.Fatalf("повторный логин: %d", resp.StatusCode)
	}

	resp, _ = u.post("/login", url.Values{"login": {"alice"}, "password": {"wrong-pass"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("неверный пароль: %d", resp.StatusCode)
	}
	resp, _ = u.post("/login", url.Values{"login": {"nobody"}, "password": {"password-1"}})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("несуществующий логин: %d", resp.StatusCode)
	}
	resp, body = u.post("/login", url.Values{"login": {"alice"}, "password": {"password-1"}, "next": {"/"}})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "alice") || !strings.Contains(body, "Добавить репозиторий") {
		t.Fatalf("вход: %d", resp.StatusCode)
	}

	// POST без CSRF отклоняется.
	jarless := u.csrf
	u.csrf = ""
	resp, _ = u.post("/projects", url.Values{"name": {"x"}, "repo_url": {e.repo}})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST без CSRF должен отклоняться: %d", resp.StatusCode)
	}
	u.csrf = jarless

	// Open redirect после входа запрещён.
	jar3, _ := cookiejar.New(nil)
	c3 := &http.Client{Jar: jar3, CheckRedirect: noRedirect.CheckRedirect}
	resp, _ = c3.PostForm(e.srv.URL+"/login", url.Values{"login": {"alice"}, "password": {"password-1"}, "next": {"//evil.example"}})
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("open redirect: %q", loc)
	}

	// Выход завершает сессию.
	u.post("/logout", url.Values{})
	if resp, _ := u.get("/"); resp.Request.URL.Path != "/login" {
		t.Fatal("после выхода страницы должны требовать вход")
	}
}

func TestProjectsIsolatedBetweenUsers(t *testing.T) {
	e := newEnv(t)
	alice := e.signup(t, "alice", "password-a")
	bob := e.signup(t, "bob", "password-b")

	resp, _ := alice.post("/projects", url.Values{"name": {"secret-repo"}, "repo_url": {e.repo}})
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Request.URL.Path, "/projects/") {
		t.Fatalf("создание через UI: %d %s", resp.StatusCode, resp.Request.URL)
	}
	e.app.Wait()
	alicePath := resp.Request.URL.Path
	rep, err := e.app.GenerateReport(context.Background(), 1, app.DefaultPeriod(30, time.Now()), 0, "manual")
	if err != nil {
		t.Fatal(err)
	}

	if _, body := alice.get("/"); !strings.Contains(body, "secret-repo") {
		t.Fatal("владелец должен видеть свой проект")
	}
	if _, body := bob.get("/"); strings.Contains(body, "secret-repo") {
		t.Fatal("чужой проект виден в списке")
	}
	// Любой доступ к чужому проекту — 404, как будто его нет.
	for _, path := range []string{alicePath, alicePath + "/reports", alicePath + "/settings", fmt.Sprintf("/reports/%d", rep.ID)} {
		if resp, _ := bob.get(path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s чужим пользователем: %d", path, resp.StatusCode)
		}
	}
	for _, path := range []string{alicePath + "/sync", alicePath + "/delete", alicePath + "/settings", alicePath + "/reports"} {
		if resp, _ := bob.post(path, url.Values{"confirm": {"secret-repo"}, "name": {"x"}, "repo_url": {e.repo},
			"from": {"2026-01-01"}, "to": {"2026-02-01"}}); resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s чужим пользователем: %d", path, resp.StatusCode)
		}
	}
	if p, err := e.app.Store.GetProject(context.Background(), 1); err != nil || p.Name != "secret-repo" {
		t.Fatalf("чужой пользователь изменил проект: %+v %v", p, err)
	}

	// Тот же репозиторий и название — самостоятельно добавленный проект Боба.
	resp, _ = bob.post("/projects", url.Values{"name": {"secret-repo"}, "repo_url": {e.repo}})
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path == alicePath {
		t.Fatalf("Боб добавляет свой проект: %d %s", resp.StatusCode, resp.Request.URL)
	}
	e.app.Wait()
	if _, body := bob.get("/"); !strings.Contains(body, "secret-repo") || !strings.Contains(body, "1 репозиторий") {
		t.Fatal("Боб должен видеть свой собственный проект")
	}

	// API по cookie-сессии тоже изолирован; сервисный токен видит всё.
	req, _ := http.NewRequest("GET", e.srv.URL+"/api/projects", nil)
	r, _ := bob.c.Do(req)
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	var list []struct{ ID int64 }
	if err := json.Unmarshal(b, &list); err != nil || len(list) != 1 || list[0].ID == 1 {
		t.Fatalf("API список Боба: %s", b)
	}
	req, _ = http.NewRequest("GET", e.srv.URL+"/api/projects/1/metrics", nil)
	if r, _ := bob.c.Do(req); r.StatusCode != http.StatusNotFound {
		t.Fatalf("API метрики чужого проекта: %d", r.StatusCode)
	}
	if _, body := e.api(t, "GET", "/api/projects", ""); strings.Count(body, `"id"`) != 2 {
		t.Fatalf("сервисный токен должен видеть все проекты: %s", body)
	}
}

func TestPagesRender(t *testing.T) {
	e := newEnv(t)
	c := e.signup(t, "owner", "password-o")
	owner, _, _ := e.app.Store.UserByLogin(context.Background(), "owner")
	p, err := e.app.CreateProject(context.Background(), owner.ID, app.ProjectInput{Name: "pages", RepoURL: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.Sync(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	authors, _ := e.app.Store.ListAuthors(context.Background(), p.ID)
	rep, _ := e.app.GenerateReport(context.Background(), p.ID, app.DefaultPeriod(30, time.Now()), 0, "manual")

	pages := map[string]string{
		"/":                  "pages",
		"/projects/1":        "Коммиты по неделям",
		"/projects/1?days=7": "Выводы",
		"/projects/1/authors/" + fmt.Sprint(authors[0].ID): "Мой вклад",
		"/projects/1/reports":                              "Сформировать отчёт",
		"/projects/1/settings":                             "Команды",
		fmt.Sprintf("/reports/%d", rep.ID):                 "Отчёт о производительности",
		"/static/app.css":                                  "--series-1",
		"/static/auth.js":                                  "password_confirm",
		"/static/fonts/golos-cyr.woff2":                    "wOF2",
	}
	for path, want := range pages {
		resp, b := c.get(path)
		if resp.StatusCode != http.StatusOK || !strings.Contains(b, want) {
			t.Errorf("%s: %d, нет %q", path, resp.StatusCode, want)
		}
	}
	for _, path := range []string{"/projects/999", "/projects/1/authors/999", "/reports/999"} {
		resp, _ := c.get(path)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: ожидался 404, got %d", path, resp.StatusCode)
		}
	}
	resp, _ := c.get("/projects/1?from=2026-05-01&to=2026-01-01")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("некорректный период: %d", resp.StatusCode)
	}
	// Цвет интерфейса: из cookie tp_accent, неизвестное значение — голубой по умолчанию.
	for accent, want := range map[string]string{"green": "green", "blue": "blue", "evil\"x": "sky", "": "sky"} {
		req, _ := http.NewRequest("GET", e.srv.URL+"/login", nil)
		if accent != "" {
			req.AddCookie(&http.Cookie{Name: "tp_accent", Value: accent})
		}
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		if !strings.Contains(string(b), `data-accent="`+want+`"`) || !strings.Contains(string(b), `data-accent-pick="green"`) {
			t.Errorf("tp_accent=%q: ожидался data-accent=%q", accent, want)
		}
	}
	resp, _ = http.Get(e.srv.URL + "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: %d", resp.StatusCode)
	}
}

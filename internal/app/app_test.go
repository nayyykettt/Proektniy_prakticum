package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"teampulse/internal/config"
	"teampulse/internal/domain"
	"teampulse/internal/provider"
	"teampulse/internal/secure"
	"teampulse/internal/storage"
	"teampulse/internal/testutil"
	"teampulse/internal/zerocracy"
)

type fakeProvider struct {
	mrs      []domain.MergeRequest
	issues   []domain.Issue
	err      error
	assigned map[int]string
}

func (f *fakeProvider) FetchMergeRequests(context.Context, time.Time) ([]domain.MergeRequest, error) {
	return f.mrs, f.err
}
func (f *fakeProvider) FetchIssues(context.Context, time.Time) ([]domain.Issue, error) {
	return f.issues, f.err
}
func (f *fakeProvider) AssignIssue(_ context.Context, iid int, login string) error {
	f.assigned[iid] = login
	return nil
}

func newApp(t *testing.T, allowLocal bool) *App {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{DataDir: dir, ReposDir: filepath.Join(dir, "repos"), AllowLocalRepos: allowLocal,
		MaxCommits: 1000, CloneTimeout: time.Minute, WorkdayStart: 9, WorkdayEnd: 19}
	store, err := storage.Open(context.Background(), filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, _ := secure.NewBox([]byte("test-secret-0123456789"))
	return New(cfg, store, box, zerocracy.DefaultPolicy(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	a := newApp(t, true)
	now := time.Now().UTC()
	r := testutil.NewRepo(t)
	r.Commit(testutil.FakeCommit{Author: "Иван", Email: "ivan@corp.ru", When: now.Add(-72 * time.Hour), Message: "feat: parser #1",
		Files: map[string]string{"internal/parser/parser.go": testutil.Lines(20, "p")}})
	r.Commit(testutil.FakeCommit{Author: "Анна", Email: "anna@corp.ru", When: now.Add(-48 * time.Hour), Message: "feat: dashboard",
		Files: map[string]string{"web/dashboard.html": testutil.Lines(10, "d")}})

	fake := &fakeProvider{assigned: map[int]string{}}
	merged := now.Add(-24 * time.Hour)
	fake.mrs = []domain.MergeRequest{{IID: 1, Title: "parser", AuthorLogin: "ivan", AuthorName: "Иван", State: "merged",
		CreatedAt: now.Add(-72 * time.Hour), MergedAt: &merged, Additions: 20,
		Reviews: []domain.Review{{AuthorLogin: "anna", AuthorName: "Анна", At: now.Add(-60 * time.Hour), Comments: 1}}}}
	fake.issues = []domain.Issue{{IID: 5, Title: "Ускорить parser", Labels: []string{"backend"}, State: "opened", CreatedAt: now.Add(-time.Hour)}}
	a.NewProvider = func(domain.Project, string) (provider.Provider, error) { return fake, nil }
	a.Cfg.AutoAssign = true

	p, err := a.CreateProject(ctx, 0, ProjectInput{Name: "demo", RepoURL: r.Dir, Provider: domain.ProviderT1Code,
		ProviderURL: "http://127.0.0.1:1", ProviderProjectID: "1", Token: "glpat-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasToken || string(p.TokenEnc) == "glpat-secret" {
		t.Fatal("токен должен храниться зашифрованным")
	}

	res, err := a.Sync(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewCommits != 2 || res.MergeRequests != 1 || res.Status != domain.SyncOK {
		t.Fatalf("sync: %+v", res)
	}
	if fake.assigned[5] != "ivan" || res.Assigned != 1 {
		t.Errorf("задача про parser должна автоматически уйти Ивану: %v", fake.assigned)
	}
	res, _ = a.Sync(ctx, p.ID)
	if res.NewCommits != 0 {
		t.Fatalf("повторная синхронизация должна быть инкрементальной: %+v", res)
	}

	d, err := a.Analyze(ctx, p.ID, DefaultPeriod(30, now), Scope{})
	if err != nil {
		t.Fatal(err)
	}
	if d.Snapshot.Summary.Commits != 2 || d.Snapshot.Summary.MRMerged != 1 || d.Snapshot.Summary.ReviewsGiven != 1 {
		t.Fatalf("метрики: %+v", d.Snapshot.Summary)
	}
	// Идентичность: коммиты Ивана (email) и его MR (логин) — один автор.
	if len(d.Snapshot.Authors) != 2 {
		t.Fatalf("ожидалось 2 разработчика, got %+v", d.Snapshot.Authors)
	}

	rep, err := a.GenerateReport(ctx, p.ID, DefaultPeriod(30, now), 0, "manual")
	if err != nil {
		t.Fatal(err)
	}
	back, _, err := a.LoadReport(ctx, rep.ID)
	if err != nil || back.Snapshot.Summary.Commits != 2 {
		t.Fatalf("отчёт: %v", err)
	}

	// Провайдер недоступен → частичная синхронизация, Git-данные сохраняются.
	fake.err = errors.New("Т1 Код недоступен")
	res, err = a.Sync(ctx, p.ID)
	if err != nil || res.Status != domain.SyncPartial {
		t.Fatalf("ожидался partial: %+v %v", res, err)
	}
	got, _ := a.Store.GetProject(ctx, p.ID)
	if got.SyncStatus != domain.SyncPartial || got.SyncError == "" {
		t.Fatalf("статус проекта: %+v", got)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	a := newApp(t, false)
	bad := []ProjectInput{
		{Name: "", RepoURL: "https://github.com/x/y"},
		{Name: "x", RepoURL: "/etc"},                       // локальные пути запрещены
		{Name: "x", RepoURL: "https://127.0.0.1/repo.git"}, // SSRF
		{Name: "x", RepoURL: "ftp://example.com/repo"},     // схема
		{Name: "x", RepoURL: "https://github.com/x/y", Branch: "a..b"},
		{Name: "x", RepoURL: "https://github.com/x/y", Provider: "t1code"}, // нет адреса инстанса
		{Name: "x", RepoURL: "https://github.com/x/y", Provider: "jira"},
	}
	for _, in := range bad {
		if _, err := a.CreateProject(ctx, 0, in); !errors.Is(err, domain.ErrInvalid) {
			t.Errorf("%+v: ожидалась ошибка валидации, got %v", in, err)
		}
	}
}

func TestSyncFailuresAndLock(t *testing.T) {
	ctx := context.Background()
	a := newApp(t, true)
	empty := testutil.NewRepo(t)
	p, err := a.CreateProject(ctx, 0, ProjectInput{Name: "empty", RepoURL: empty.Dir})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Sync(ctx, p.ID)
	if err != nil || res.NewCommits != 0 || len(res.Warnings) == 0 {
		t.Fatalf("пустой репозиторий: %+v %v", res, err)
	}

	r := testutil.NewRepo(t)
	r.Commit(testutil.FakeCommit{Author: "A", Email: "a@x", When: time.Now(), Message: "m", Files: map[string]string{"a": "1"}})
	p2, _ := a.CreateProject(ctx, 0, ProjectInput{Name: "branch", RepoURL: r.Dir, Branch: "nope"})
	if _, err := a.Sync(ctx, p2.ID); err == nil {
		t.Fatal("несуществующая ветка должна давать ошибку")
	}
	got, _ := a.Store.GetProject(ctx, p2.ID)
	if got.SyncStatus != domain.SyncFailed {
		t.Fatalf("статус: %s", got.SyncStatus)
	}

	a.acquire(p2.ID)
	if err := a.SyncAsync(p2.ID); !errors.Is(err, domain.ErrSyncActive) {
		t.Fatalf("повторный запуск должен блокироваться: %v", err)
	}
	a.release(p2.ID)

	if _, err := a.Analyze(ctx, p2.ID, domain.Period{From: time.Now(), To: time.Now().Add(-time.Hour)}, Scope{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("from > to: %v", err)
	}
}

func TestParsePeriod(t *testing.T) {
	now := time.Date(2026, 9, 29, 15, 0, 0, 0, time.UTC)
	p, err := ParsePeriod("2026-09-01", "2026-09-10", now)
	if err != nil || p.From.Day() != 1 || p.To.Day() != 11 {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := ParsePeriod("2026-09-10", "2026-09-01", now); err == nil {
		t.Fatal("from > to")
	}
	if _, err := ParsePeriod("10.09.2026", "", now); err == nil {
		t.Fatal("неверный формат")
	}
	p, _ = ParsePeriod("", "", now)
	if p.To.Day() != 30 || p.To.Sub(p.From) != 30*24*time.Hour {
		t.Fatalf("период по умолчанию: %+v", p)
	}
}

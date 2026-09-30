package scheduler

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"teampulse/internal/app"
	"teampulse/internal/config"
	"teampulse/internal/secure"
	"teampulse/internal/storage"
	"teampulse/internal/testutil"
	"teampulse/internal/zerocracy"
)

func TestRunOnceSyncsAndReports(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := &config.Config{ReposDir: filepath.Join(dir, "repos"), AllowLocalRepos: true, MaxCommits: 100,
		CloneTimeout: time.Minute, WorkdayStart: 9, WorkdayEnd: 19, SyncInterval: time.Hour, ReportInterval: 7 * 24 * time.Hour}
	store, err := storage.Open(ctx, filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	box, _ := secure.NewBox([]byte("0123456789abcdef!"))
	a := app.New(cfg, store, box, zerocracy.DefaultPolicy(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	r := testutil.NewRepo(t)
	r.Commit(testutil.FakeCommit{Author: "A", Email: "a@x", When: time.Now().Add(-time.Hour), Message: "feat: a", Files: map[string]string{"a.go": "1"}})
	p, err := a.CreateProject(ctx, 0, app.ProjectInput{Name: "p", RepoURL: r.Dir})
	if err != nil {
		t.Fatal(err)
	}

	RunOnce(ctx, a, time.Now())
	got, _ := store.GetProject(ctx, p.ID)
	if got.LastSyncAt == nil {
		t.Fatal("проект должен синхронизироваться автоматически")
	}
	reports, _ := store.ListReports(ctx, p.ID, 10)
	if len(reports) != 1 || reports[0].Trigger != TriggerSchedule {
		t.Fatalf("ожидался автоматический отчёт: %+v", reports)
	}

	// Повторный проход сразу — ничего не делает.
	RunOnce(ctx, a, time.Now())
	reports, _ = store.ListReports(ctx, p.ID, 10)
	if len(reports) != 1 {
		t.Fatalf("отчёт не должен дублироваться: %d", len(reports))
	}
	// Через неделю — новый отчёт.
	RunOnce(ctx, a, time.Now().Add(8*24*time.Hour))
	reports, _ = store.ListReports(ctx, p.ID, 10)
	if len(reports) != 2 {
		t.Fatalf("ожидался второй отчёт: %d", len(reports))
	}
}

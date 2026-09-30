//go:build integration

// Интеграционный сценарий с реальным публичным репозиторием (требование ТЗ, спринт 3).
// Запуск: go test -tags integration ./internal/app/ (нужен доступ в интернет).
package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"teampulse/internal/domain"
	"teampulse/internal/report"
)

func TestRealRepository(t *testing.T) {
	repoURL := os.Getenv("TP_IT_REPO")
	if repoURL == "" {
		repoURL = "https://github.com/go-git/go-billy"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	a := newApp(t, false) // как в продакшене: только https, защита от SSRF

	p, err := a.CreateProject(ctx, 0, ProjectInput{Name: "real", RepoURL: repoURL})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Sync(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewCommits < 50 || res.Status != domain.SyncOK {
		t.Fatalf("ожидалась полная загрузка истории: %+v", res)
	}
	t.Logf("загружено коммитов: %d за %s", res.NewCommits, res.Duration)

	// Повторная синхронизация идёт через fetch и ничего не дублирует.
	res, err = a.Sync(ctx, p.ID)
	if err != nil || res.NewCommits != 0 {
		t.Fatalf("инкрементальная синхронизация: %+v %v", res, err)
	}

	period := domain.Period{From: time.Now().AddDate(-5, 0, 0), To: time.Now()}
	d, err := a.Analyze(ctx, p.ID, period, Scope{})
	if err != nil {
		t.Fatal(err)
	}
	s := d.Snapshot.Summary
	if s.Commits == 0 || s.ActiveAuthors == 0 || s.MedianCommitSize == nil || s.FilesTracked == 0 || len(d.Snapshot.Hotspots) == 0 {
		t.Fatalf("метрики не рассчитаны: %+v", s)
	}
	for _, au := range d.Snapshot.Authors {
		if strings.Contains(au.Name, "[bot]") {
			t.Errorf("боты должны исключаться из метрик: %s", au.Name)
		}
	}

	rep, err := a.GenerateReport(ctx, p.ID, period, 0, "manual")
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := a.LoadReport(ctx, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	for format := range report.Formats {
		if _, err := report.Bytes(loaded, format); err != nil {
			t.Errorf("отчёт %s: %v", format, err)
		}
	}
}

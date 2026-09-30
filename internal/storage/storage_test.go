package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"teampulse/internal/domain"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProjectCRUD(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	p := &domain.Project{Name: "demo", RepoURL: "https://example.com/r.git", Provider: domain.ProviderNone, TokenEnc: []byte{1, 2}}
	if err := s.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateProject(ctx, &domain.Project{Name: "demo", RepoURL: "x"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ожидался конфликт, got %v", err)
	}
	got, err := s.GetProject(ctx, p.ID)
	if err != nil || got.Name != "demo" || !got.HasToken || got.SyncStatus != domain.SyncNever {
		t.Fatalf("got %+v, %v", got, err)
	}
	if err := s.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetProject(ctx, p.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ожидалось ErrNotFound, got %v", err)
	}
}

func TestAuthorIdentityLinksGitAndProvider(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	p := &domain.Project{Name: "p", RepoURL: "x", Provider: domain.ProviderT1Code}
	if err := s.CreateProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	commits := []domain.Commit{
		{SHA: "a1", AuthorName: "Иван Петров", AuthorEmail: "Ivan@Corp.ru", AuthoredAt: at,
			Files: []domain.FileChange{{Path: "main.go", Additions: 10}}},
		{SHA: "a2", AuthorName: "Иван Петров", AuthorEmail: "ivan@corp.ru", AuthoredAt: at.Add(time.Hour)},
	}
	if err := s.SaveCommits(ctx, p.ID, commits); err != nil {
		t.Fatal(err)
	}
	// Повторная загрузка тех же коммитов не создаёт дублей.
	if err := s.SaveCommits(ctx, p.ID, commits); err != nil {
		t.Fatal(err)
	}
	mr := domain.MergeRequest{IID: 1, Title: "feat", AuthorLogin: "ipetrov", AuthorName: "Иван Петров", State: "merged",
		CreatedAt: at, Additions: 5, Deletions: 1,
		Reviews: []domain.Review{{AuthorLogin: "anna", AuthorName: "Анна", At: at.Add(time.Hour), Comments: 2}}}
	if err := s.SaveMergeRequests(ctx, p.ID, []domain.MergeRequest{mr}); err != nil {
		t.Fatal(err)
	}
	authors, _ := s.ListAuthors(ctx, p.ID)
	if len(authors) != 2 {
		t.Fatalf("ожидалось 2 автора (Иван, Анна), got %+v", authors)
	}
	ds, err := s.LoadDataset(ctx, p.ID, at.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds.Commits) != 2 || len(ds.Commits[0].Files) != 1 {
		t.Fatalf("commits: %+v", ds.Commits)
	}
	if ds.MergeRequests[0].AuthorID != ds.Commits[0].AuthorID {
		t.Fatal("MR и коммит одного человека должны указывать на одного автора")
	}
	if len(ds.MergeRequests[0].Reviews) != 1 {
		t.Fatal("ревью не сохранено")
	}

	// Слияние авторов переносит активность.
	var ivan, anna int64
	for _, a := range authors {
		if a.Login == "anna" {
			anna = a.ID
		} else {
			ivan = a.ID
		}
	}
	if err := s.MergeAuthors(ctx, p.ID, ivan, anna); err != nil {
		t.Fatal(err)
	}
	authors, _ = s.ListAuthors(ctx, p.ID)
	if len(authors) != 1 {
		t.Fatalf("после слияния ожидался 1 автор, got %d", len(authors))
	}
}

func TestTeamsAndReports(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	p := &domain.Project{Name: "p", RepoURL: "x"}
	_ = s.CreateProject(ctx, p)
	_ = s.SaveCommits(ctx, p.ID, []domain.Commit{{SHA: "a", AuthorName: "A", AuthorEmail: "a@x", AuthoredAt: time.Now()}})
	authors, _ := s.ListAuthors(ctx, p.ID)

	team := &domain.Team{ProjectID: p.ID, Name: "Backend", MemberIDs: []int64{authors[0].ID}}
	if err := s.SaveTeam(ctx, team); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTeam(ctx, &domain.Team{ProjectID: p.ID, Name: "X", MemberIDs: []int64{9999}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("чужой автор должен отклоняться, got %v", err)
	}
	got, err := s.GetTeam(ctx, p.ID, team.ID)
	if err != nil || len(got.MemberIDs) != 1 {
		t.Fatalf("team %+v %v", got, err)
	}

	r := &domain.StoredReport{ProjectID: p.ID, From: time.Now().Add(-time.Hour), To: time.Now(), Trigger: "manual", Payload: []byte(`{}`)}
	if err := s.SaveReport(ctx, r); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListReports(ctx, p.ID, 10)
	if len(list) != 1 || list[0].Payload != nil {
		t.Fatalf("list %+v", list)
	}
	full, err := s.GetReport(ctx, r.ID)
	if err != nil || string(full.Payload) != `{}` {
		t.Fatalf("report %+v %v", full, err)
	}
}

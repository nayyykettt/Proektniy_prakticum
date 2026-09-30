package gitparse

import (
	"context"
	"testing"
	"time"

	"teampulse/internal/testutil"
)

func TestParseIncremental(t *testing.T) {
	ctx := context.Background()
	r := testutil.NewRepo(t)
	msk := time.FixedZone("MSK", 3*3600)
	base := time.Date(2026, 3, 2, 10, 0, 0, 0, msk)

	r.Commit(testutil.FakeCommit{Author: "Иван", Email: "Ivan@Corp.RU", When: base, Message: "feat: init",
		Files: map[string]string{"main.go": testutil.Lines(10, "x"), "go.sum": testutil.Lines(50, "h")}})
	r.Commit(testutil.FakeCommit{Author: "Анна", Email: "anna@corp.ru", When: base.Add(time.Hour), Message: "test: add",
		Files: map[string]string{"main_test.go": testutil.Lines(4, "t")}})

	repo, err := Open(ctx, r.Dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	head, err := ResolveBranch(repo, "")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Parse(ctx, repo, head, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 2 {
		t.Fatalf("ожидалось 2 коммита, got %d", len(res.Commits))
	}
	first := res.Commits[1] // лог идёт от новых к старым
	if first.AuthorEmail != "ivan@corp.ru" || first.TZOffset != 180 {
		t.Fatalf("нормализация автора/часового пояса: %+v", first)
	}
	if first.Additions != 10 || first.FilesChanged != 1 || len(first.Files) != 2 {
		t.Fatalf("go.sum должен исключаться из подсчёта строк: %+v", first)
	}
	if !res.Commits[0].Files[0].IsTest {
		t.Fatal("main_test.go должен распознаваться как тест")
	}

	// Инкрементальность: известные коммиты пропускаются.
	known := map[string]bool{res.Commits[0].SHA: true, res.Commits[1].SHA: true}
	r.Commit(testutil.FakeCommit{Author: "Иван", Email: "ivan@corp.ru", When: base.Add(2 * time.Hour), Message: "fix",
		Files: map[string]string{"main.go": testutil.Lines(12, "x")}})
	head, _ = ResolveBranch(repo, "")
	res, err = Parse(ctx, repo, head, ParseOptions{Known: known})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Commits) != 1 || res.Commits[0].Message != "fix" {
		t.Fatalf("ожидался 1 новый коммит, got %+v", res.Commits)
	}
}

func TestParseLimitsAndBadDates(t *testing.T) {
	ctx := context.Background()
	r := testutil.NewRepo(t)
	now := time.Now()
	r.Commit(testutil.FakeCommit{Author: "A", Email: "a@x", When: time.Unix(0, 0), Message: "1970",
		Files: map[string]string{"a": "1"}})
	for i := range 3 {
		r.Commit(testutil.FakeCommit{Author: "A", Email: "a@x", When: now.Add(-time.Duration(3-i) * time.Hour),
			Message: "c", Files: map[string]string{"a": testutil.Lines(i+2, "a")}})
	}
	repo, _ := Open(ctx, r.Dir, "", "")
	head, _ := ResolveBranch(repo, "")

	res, err := Parse(ctx, repo, head, ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 1 || len(res.Commits) != 3 {
		t.Fatalf("коммит 1970 года должен пропускаться: skipped=%d commits=%d", res.Skipped, len(res.Commits))
	}
	res, _ = Parse(ctx, repo, head, ParseOptions{MaxCommits: 2})
	if !res.Truncated || len(res.Commits) != 2 {
		t.Fatalf("лимит: truncated=%v commits=%d", res.Truncated, len(res.Commits))
	}
}

func TestResolveUnknownBranch(t *testing.T) {
	r := testutil.NewRepo(t)
	r.Commit(testutil.FakeCommit{Author: "A", Email: "a@x", When: time.Now(), Message: "m", Files: map[string]string{"a": "1"}})
	repo, _ := Open(context.Background(), r.Dir, "", "")
	if _, err := ResolveBranch(repo, "nope"); err == nil {
		t.Fatal("ожидалась ошибка для несуществующей ветки")
	}
	empty := testutil.NewRepo(t)
	repo, _ = Open(context.Background(), empty.Dir, "", "")
	if _, err := ResolveBranch(repo, ""); err != ErrEmptyRepo {
		t.Fatalf("пустой репозиторий: got %v", err)
	}
}

func TestClassifier(t *testing.T) {
	c := NewClassifier([]string{"docs/", "*.md"})
	cases := map[string]bool{
		"vendor/lib/a.go": true, "web/node_modules/x.js": true, "go.sum": true, "app/yarn.lock": true,
		"api/v1/api.pb.go": true, "static/app.min.js": true, "docs/readme.txt": true, "README.md": true,
		"main.go": false, "internal/vendorx/a.go": false, "build.go": false,
	}
	for p, want := range cases {
		if got := c.Excluded(p); got != want {
			t.Errorf("Excluded(%q)=%v, want %v", p, got, want)
		}
	}
	tests := map[string]bool{
		"a_test.go": true, "src/__tests__/a.js": true, "x.spec.ts": true, "tests/a.py": true, "test_a.py": true,
		"src/FooTest.java": true, "main.go": false, "contest.go": false, "attestation/a.go": false,
	}
	for p, want := range tests {
		if got := IsTest(p); got != want {
			t.Errorf("IsTest(%q)=%v, want %v", p, got, want)
		}
	}
}

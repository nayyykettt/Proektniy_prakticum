// Package testutil — вспомогательные средства для тестов: детерминированные Git-репозитории.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// FakeCommit описывает коммит тестового репозитория.
type FakeCommit struct {
	Author  string
	Email   string
	When    time.Time
	Message string
	Files   map[string]string // путь → полное новое содержимое
}

// Repo — тестовый репозиторий с рабочей копией.
type Repo struct {
	t    *testing.T
	Dir  string
	Repo *git.Repository
}

// NewRepo создаёт пустой репозиторий во временном каталоге.
func NewRepo(t *testing.T) *Repo {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return &Repo{t: t, Dir: dir, Repo: r}
}

// Commit записывает файлы и создаёт коммит; возвращает SHA.
func (r *Repo) Commit(c FakeCommit) string {
	r.t.Helper()
	wt, err := r.Repo.Worktree()
	if err != nil {
		r.t.Fatal(err)
	}
	for p, content := range c.Files {
		full := filepath.Join(r.Dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			r.t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			r.t.Fatal(err)
		}
		if _, err := wt.Add(p); err != nil {
			r.t.Fatal(err)
		}
	}
	sig := &object.Signature{Name: c.Author, Email: c.Email, When: c.When}
	h, err := wt.Commit(c.Message, &git.CommitOptions{Author: sig, Committer: sig, AllowEmptyCommits: true})
	if err != nil {
		r.t.Fatal(err)
	}
	return h.String()
}

// Lines возвращает n строк текста (для управляемого размера изменений).
func Lines(n int, prefix string) string {
	s := ""
	for i := range n {
		s += prefix + " line " + string(rune('a'+i%26)) + "\n"
	}
	return s
}

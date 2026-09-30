package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"teampulse/internal/domain"
)

func TestUsersAndProjectOwnership(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	orphan := &domain.Project{Name: "legacy", RepoURL: "x"}
	if err := s.CreateProject(ctx, orphan); err != nil {
		t.Fatal(err)
	}
	alice, err := s.CreateUser(ctx, "alice", "hash-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, "ALICE", "hash"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("логин без учёта регистра должен быть уникален: %v", err)
	}
	bob, err := s.CreateUser(ctx, "bob", "hash-b")
	if err != nil {
		t.Fatal(err)
	}
	// Первый пользователь забирает проекты без владельца, второй — нет.
	if got, _ := s.GetProject(ctx, orphan.ID); got.OwnerID != alice.ID {
		t.Fatalf("проект без владельца должен перейти первому пользователю: owner=%d", got.OwnerID)
	}
	// Одинаковые названия у разных владельцев допустимы, у одного — конфликт.
	if err := s.CreateProject(ctx, &domain.Project{OwnerID: bob.ID, Name: "legacy", RepoURL: "x"}); err != nil {
		t.Fatalf("у другого пользователя то же название допустимо: %v", err)
	}
	if err := s.CreateProject(ctx, &domain.Project{OwnerID: alice.ID, Name: "legacy", RepoURL: "x"}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ожидался конфликт названия у одного владельца: %v", err)
	}
	for _, c := range []struct {
		user int64
		want int
	}{{alice.ID, 1}, {bob.ID, 1}, {999, 0}} {
		ps, err := s.ListUserProjects(ctx, c.user)
		if err != nil || len(ps) != c.want {
			t.Fatalf("user %d: %d проектов, %v", c.user, len(ps), err)
		}
		for _, p := range ps {
			if p.OwnerID != c.user {
				t.Fatalf("чужой проект в списке пользователя %d", c.user)
			}
		}
	}
	u, hash, err := s.UserByLogin(ctx, "Bob")
	if err != nil || u.ID != bob.ID || hash != "hash-b" {
		t.Fatalf("UserByLogin: %+v %q %v", u, hash, err)
	}
	if _, _, err := s.UserByLogin(ctx, "nobody"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("ожидалось ErrNotFound: %v", err)
	}
}

// Миграция 002 пересоздаёт projects; связанные данные не должны удалиться каскадно.
func TestMigrationKeepsExistingData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := migrations.ReadFile("migrations/001_init.sql")
	for _, q := range []string{
		`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`, string(v1), `INSERT INTO schema_migrations VALUES (1)`,
		`INSERT INTO projects (id, name, repo_url, created_at) VALUES (7, 'old', 'x', 1)`,
		`INSERT INTO authors (id, project_id, name) VALUES (1, 7, 'Иван')`,
		`INSERT INTO commits (project_id, sha, author_id, authored_at, message) VALUES (7, 'abc', 1, 1, 'init')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	known, err := s.KnownSHAs(ctx, 7)
	if err != nil || !known["abc"] {
		t.Fatalf("коммиты потеряны при миграции: %v %v", known, err)
	}
	p, err := s.GetProject(ctx, 7)
	if err != nil || p.OwnerID != 0 {
		t.Fatalf("проект: %+v %v", p, err)
	}
	var fk int
	if err := s.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("внешние ключи должны быть снова включены: %d %v", fk, err)
	}
}

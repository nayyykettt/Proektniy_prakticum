package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"teampulse/internal/domain"
)

// resolveAuthor находит автора по любой из известных идентичностей
// (email → логин → имя) или создаёт нового, дописывая недостающие алиасы.
// Так коммит из Git (email) и MR из Т1 Код (логин) попадают к одному человеку.
func resolveAuthor(ctx context.Context, tx *sql.Tx, projectID int64, name, email, login string) (int64, error) {
	name = strings.TrimSpace(name)
	keys := []struct{ kind, value string }{
		{"email", strings.ToLower(strings.TrimSpace(email))},
		{"login", strings.ToLower(strings.TrimSpace(login))},
		{"name", strings.ToLower(name)},
	}
	var id int64
	for _, k := range keys {
		if k.value == "" {
			continue
		}
		err := tx.QueryRowContext(ctx, `SELECT author_id FROM author_aliases WHERE project_id=? AND kind=? AND value=?`,
			projectID, k.kind, k.value).Scan(&id)
		if err == nil {
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
	}
	if id == 0 {
		if name == "" {
			name = firstNonEmpty(login, email, "unknown")
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO authors(project_id, name, email, login) VALUES (?, ?, ?, ?)`,
			projectID, name, keys[0].value, keys[1].value)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	} else {
		// Дополняем карточку автора, если узнали email или логин.
		if _, err := tx.ExecContext(ctx, `UPDATE authors SET
			email = CASE WHEN email = '' THEN ? ELSE email END,
			login = CASE WHEN login = '' THEN ? ELSE login END WHERE id=?`, keys[0].value, keys[1].value, id); err != nil {
			return 0, err
		}
	}
	for _, k := range keys {
		if k.value == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO author_aliases(project_id, kind, value, author_id) VALUES (?, ?, ?, ?)`,
			projectID, k.kind, k.value, id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ListAuthors возвращает авторов проекта.
func (s *Store) ListAuthors(ctx context.Context, projectID int64) ([]domain.Author, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, email, login FROM authors WHERE project_id=? ORDER BY name COLLATE NOCASE`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Author
	for rows.Next() {
		var a domain.Author
		if err := rows.Scan(&a.ID, &a.Name, &a.Email, &a.Login); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MergeAuthors переносит всю активность автора src на dst (разные email одного человека).
func (s *Store) MergeAuthors(ctx context.Context, projectID, dst, src int64) error {
	if dst == src {
		return fmt.Errorf("%w: нельзя объединить автора с самим собой", domain.ErrInvalid)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM authors WHERE project_id=? AND id IN (?, ?)`, projectID, dst, src).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			return domain.ErrNotFound
		}
		stmts := []string{
			`UPDATE commits SET author_id=? WHERE author_id=?`,
			`UPDATE merge_requests SET author_id=? WHERE author_id=?`,
			`UPDATE issues SET assignee_id=? WHERE assignee_id=?`,
			`UPDATE OR IGNORE mr_reviews SET author_id=? WHERE author_id=?`,
			`UPDATE OR IGNORE author_aliases SET author_id=? WHERE author_id=?`,
			`UPDATE OR IGNORE team_members SET author_id=? WHERE author_id=?`,
		}
		for _, q := range stmts {
			if _, err := tx.ExecContext(ctx, q, dst, src); err != nil {
				return err
			}
		}
		// Остатки (дубли ревью/членства) удаляются каскадно вместе с автором.
		if _, err := tx.ExecContext(ctx, `DELETE FROM mr_reviews WHERE author_id=?`, src); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM authors WHERE id=?`, src)
		return err
	})
}

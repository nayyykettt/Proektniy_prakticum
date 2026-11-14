package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"teampulse/internal/domain"
)

// CreateUser регистрирует пользователя без доступа к существующим проектам.
func (s *Store) CreateUser(ctx context.Context, login, passwordHash string) (domain.User, error) {
	u := domain.User{Login: login, CreatedAt: time.Now().UTC()}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO users (login, password_hash, created_at) VALUES (?, ?, ?)`,
			login, passwordHash, unix(u.CreatedAt))
		if err != nil {
			if isUnique(err) {
				return fmt.Errorf("%w: логин %q уже занят", domain.ErrConflict, login)
			}
			return err
		}
		if u.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return err
	})
	return u, err
}

// GetUser возвращает пользователя по ID.
func (s *Store) GetUser(ctx context.Context, id int64) (domain.User, error) {
	u, _, err := scanUser(s.db.QueryRowContext(ctx, `SELECT id, login, password_hash, created_at FROM users WHERE id=?`, id))
	return u, err
}

// UserByLogin возвращает пользователя и хеш его пароля (логин без учёта регистра).
func (s *Store) UserByLogin(ctx context.Context, login string) (domain.User, string, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id, login, password_hash, created_at FROM users WHERE login=?`, login))
}

func scanUser(row *sql.Row) (domain.User, string, error) {
	var u domain.User
	var hash string
	var created int64
	err := row.Scan(&u.ID, &u.Login, &hash, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return u, "", domain.ErrNotFound
	}
	u.CreatedAt = fromUnix(created)
	return u, hash, err
}

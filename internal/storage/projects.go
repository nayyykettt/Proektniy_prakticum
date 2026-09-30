package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"teampulse/internal/domain"
)

const projectCols = `id, owner_id, name, repo_url, branch, provider, provider_url, provider_project_id,
	token_enc, last_synced_sha, sync_status, sync_error, last_sync_at, created_at`

func scanProject(row interface{ Scan(...any) error }) (domain.Project, error) {
	var p domain.Project
	var lastSync, owner sql.NullInt64
	var created int64
	err := row.Scan(&p.ID, &owner, &p.Name, &p.RepoURL, &p.Branch, &p.Provider, &p.ProviderURL,
		&p.ProviderProjectID, &p.TokenEnc, &p.LastSyncedSHA, &p.SyncStatus, &p.SyncError, &lastSync, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.ErrNotFound
	}
	p.OwnerID = owner.Int64
	p.LastSyncAt = fromNull(lastSync)
	p.CreatedAt = fromUnix(created)
	p.HasToken = len(p.TokenEnc) > 0
	return p, err
}

// CreateProject сохраняет новый проект. Название уникально в пределах владельца.
func (s *Store) CreateProject(ctx context.Context, p *domain.Project) error {
	p.CreatedAt = time.Now().UTC()
	p.SyncStatus = domain.SyncNever
	res, err := s.db.ExecContext(ctx, `INSERT INTO projects
		(owner_id, name, repo_url, branch, provider, provider_url, provider_project_id, token_enc, sync_status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nullID(p.OwnerID), p.Name, p.RepoURL, p.Branch, p.Provider, p.ProviderURL, p.ProviderProjectID, p.TokenEnc, p.SyncStatus, unix(p.CreatedAt))
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: проект с именем %q уже существует", domain.ErrConflict, p.Name)
		}
		return err
	}
	p.ID, err = res.LastInsertId()
	p.HasToken = len(p.TokenEnc) > 0
	return err
}

// UpdateProject сохраняет изменяемые настройки проекта.
func (s *Store) UpdateProject(ctx context.Context, p *domain.Project) error {
	res, err := s.db.ExecContext(ctx, `UPDATE projects SET name=?, repo_url=?, branch=?, provider=?,
		provider_url=?, provider_project_id=?, token_enc=? WHERE id=?`,
		p.Name, p.RepoURL, p.Branch, p.Provider, p.ProviderURL, p.ProviderProjectID, p.TokenEnc, p.ID)
	if err != nil {
		if isUnique(err) {
			return fmt.Errorf("%w: проект с именем %q уже существует", domain.ErrConflict, p.Name)
		}
		return err
	}
	return mustAffect(res)
}

// GetProject возвращает проект по ID.
func (s *Store) GetProject(ctx context.Context, id int64) (domain.Project, error) {
	return scanProject(s.db.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id=?`, id))
}

// ListProjects возвращает все проекты (для планировщика, CLI и сервисного API-токена).
func (s *Store) ListProjects(ctx context.Context) ([]domain.Project, error) {
	return s.listProjects(ctx, `SELECT `+projectCols+` FROM projects ORDER BY name`)
}

// ListUserProjects возвращает только проекты указанного пользователя.
func (s *Store) ListUserProjects(ctx context.Context, ownerID int64) ([]domain.Project, error) {
	return s.listProjects(ctx, `SELECT `+projectCols+` FROM projects WHERE owner_id=? ORDER BY name`, ownerID)
}

func (s *Store) listProjects(ctx context.Context, q string, args ...any) ([]domain.Project, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProject удаляет проект и все его данные (каскадно).
func (s *Store) DeleteProject(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM projects WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SetSyncState обновляет статус синхронизации.
func (s *Store) SetSyncState(ctx context.Context, id int64, status, syncErr, lastSHA string) error {
	q := `UPDATE projects SET sync_status=?, sync_error=?`
	args := []any{status, syncErr}
	if status != domain.SyncRunning {
		q += `, last_sync_at=?`
		args = append(args, time.Now().Unix())
	}
	if lastSHA != "" {
		q += `, last_synced_sha=?`
		args = append(args, lastSHA)
	}
	q += ` WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, append(args, id)...)
	return err
}

// ResetRunningSyncs сбрасывает зависшие статусы после рестарта сервиса.
func (s *Store) ResetRunningSyncs(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET sync_status=?, sync_error=? WHERE sync_status=?`,
		domain.SyncFailed, "синхронизация прервана перезапуском сервиса", domain.SyncRunning)
	return err
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func mustAffect(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

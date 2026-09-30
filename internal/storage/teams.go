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

// SaveTeam создаёт команду (ID == 0) или заменяет её состав.
func (s *Store) SaveTeam(ctx context.Context, t *domain.Team) error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return fmt.Errorf("%w: пустое название команды", domain.ErrInvalid)
	}
	return s.tx(ctx, func(tx *sql.Tx) error {
		if t.ID == 0 {
			res, err := tx.ExecContext(ctx, `INSERT INTO teams(project_id, name) VALUES (?, ?)`, t.ProjectID, t.Name)
			if err != nil {
				if isUnique(err) {
					return fmt.Errorf("%w: команда %q уже есть", domain.ErrConflict, t.Name)
				}
				return err
			}
			if t.ID, err = res.LastInsertId(); err != nil {
				return err
			}
		} else {
			res, err := tx.ExecContext(ctx, `UPDATE teams SET name=? WHERE id=? AND project_id=?`, t.Name, t.ID, t.ProjectID)
			if err != nil {
				return err
			}
			if err := mustAffect(res); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM team_members WHERE team_id=?`, t.ID); err != nil {
				return err
			}
		}
		for _, aid := range t.MemberIDs {
			// Добавляем только авторов этого же проекта.
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO team_members(team_id, author_id)
				SELECT ?, id FROM authors WHERE id=? AND project_id=?`, t.ID, aid, t.ProjectID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("%w: автор %d не найден в проекте", domain.ErrInvalid, aid)
			}
		}
		return nil
	})
}

// ListTeams возвращает команды проекта с составом.
func (s *Store) ListTeams(ctx context.Context, projectID int64) ([]domain.Team, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT t.id, t.name, m.author_id FROM teams t
		LEFT JOIN team_members m ON m.team_id = t.id WHERE t.project_id=? ORDER BY t.name, m.author_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Team
	for rows.Next() {
		var id int64
		var name string
		var member sql.NullInt64
		if err := rows.Scan(&id, &name, &member); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != id {
			out = append(out, domain.Team{ID: id, ProjectID: projectID, Name: name, MemberIDs: []int64{}})
		}
		if member.Valid {
			out[len(out)-1].MemberIDs = append(out[len(out)-1].MemberIDs, member.Int64)
		}
	}
	return out, rows.Err()
}

// GetTeam возвращает команду проекта.
func (s *Store) GetTeam(ctx context.Context, projectID, teamID int64) (domain.Team, error) {
	teams, err := s.ListTeams(ctx, projectID)
	if err != nil {
		return domain.Team{}, err
	}
	for _, t := range teams {
		if t.ID == teamID {
			return t, nil
		}
	}
	return domain.Team{}, domain.ErrNotFound
}

// DeleteTeam удаляет команду.
func (s *Store) DeleteTeam(ctx context.Context, projectID, teamID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM teams WHERE id=? AND project_id=?`, teamID, projectID)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// SaveReport сохраняет сгенерированный отчёт.
func (s *Store) SaveReport(ctx context.Context, r *domain.StoredReport) error {
	r.GeneratedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `INSERT INTO reports(project_id, team_id, from_ts, to_ts, generated_at, trigger, payload)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, r.ProjectID, r.TeamID, unix(r.From), unix(r.To), unix(r.GeneratedAt), r.Trigger, r.Payload)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

const reportCols = `id, project_id, team_id, from_ts, to_ts, generated_at, trigger`

func scanReport(row interface{ Scan(...any) error }, withPayload bool) (domain.StoredReport, error) {
	var r domain.StoredReport
	var from, to, gen int64
	dest := []any{&r.ID, &r.ProjectID, &r.TeamID, &from, &to, &gen, &r.Trigger}
	if withPayload {
		dest = append(dest, &r.Payload)
	}
	err := row.Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return r, domain.ErrNotFound
	}
	r.From, r.To, r.GeneratedAt = fromUnix(from), fromUnix(to), fromUnix(gen)
	return r, err
}

// GetReport возвращает отчёт с данными.
func (s *Store) GetReport(ctx context.Context, id int64) (domain.StoredReport, error) {
	return scanReport(s.db.QueryRowContext(ctx, `SELECT `+reportCols+`, payload FROM reports WHERE id=?`, id), true)
}

// ListReports возвращает историю отчётов проекта (без данных).
func (s *Store) ListReports(ctx context.Context, projectID int64, limit int) ([]domain.StoredReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+reportCols+` FROM reports WHERE project_id=?
		ORDER BY generated_at DESC, id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StoredReport
	for rows.Next() {
		r, err := scanReport(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastReportAt — время последнего автоматического отчёта проекта.
func (s *Store) LastReportAt(ctx context.Context, projectID int64, trigger string) (time.Time, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(generated_at) FROM reports WHERE project_id=? AND trigger=?`,
		projectID, trigger).Scan(&v)
	if err != nil || !v.Valid {
		return time.Time{}, err
	}
	return fromUnix(v.Int64), nil
}

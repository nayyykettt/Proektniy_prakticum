package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"teampulse/internal/domain"
)

const commitBatch = 500

// KnownSHAs возвращает множество уже загруженных коммитов (для инкрементальной синхронизации).
func (s *Store) KnownSHAs(ctx context.Context, projectID int64) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sha FROM commits WHERE project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out[sha] = true
	}
	return out, rows.Err()
}

// SaveCommits сохраняет коммиты с файлами пачками; повторы игнорируются.
func (s *Store) SaveCommits(ctx context.Context, projectID int64, commits []domain.Commit) error {
	for start := 0; start < len(commits); start += commitBatch {
		end := min(start+commitBatch, len(commits))
		err := s.tx(ctx, func(tx *sql.Tx) error {
			for i := start; i < end; i++ {
				c := &commits[i]
				id, err := resolveAuthor(ctx, tx, projectID, c.AuthorName, c.AuthorEmail, "")
				if err != nil {
					return err
				}
				c.AuthorID = id
				res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO commits
					(project_id, sha, author_id, authored_at, tz_offset, message, is_merge, additions, deletions, files_changed)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
					projectID, c.SHA, id, unix(c.AuthoredAt), c.TZOffset, c.Message, boolInt(c.IsMerge),
					c.Additions, c.Deletions, c.FilesChanged)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					continue
				}
				for _, f := range c.Files {
					if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO file_changes
						(project_id, sha, path, additions, deletions, is_test, excluded) VALUES (?, ?, ?, ?, ?, ?, ?)`,
						projectID, c.SHA, f.Path, f.Additions, f.Deletions, boolInt(f.IsTest), boolInt(f.Excluded)); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveMergeRequests добавляет или обновляет MR вместе с ревью.
func (s *Store) SaveMergeRequests(ctx context.Context, projectID int64, mrs []domain.MergeRequest) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, mr := range mrs {
			aid, err := resolveAuthor(ctx, tx, projectID, mr.AuthorName, "", mr.AuthorLogin)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO merge_requests
				(project_id, iid, title, author_id, state, created_at, merged_at, closed_at, first_review_at, additions, deletions, comments)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(project_id, iid) DO UPDATE SET title=excluded.title, author_id=excluded.author_id,
					state=excluded.state, merged_at=excluded.merged_at, closed_at=excluded.closed_at,
					first_review_at=excluded.first_review_at, additions=excluded.additions,
					deletions=excluded.deletions, comments=excluded.comments`,
				projectID, mr.IID, mr.Title, aid, mr.State, unix(mr.CreatedAt), unixPtr(mr.MergedAt), unixPtr(mr.ClosedAt),
				unixPtr(mr.FirstReviewAt), mr.Additions, mr.Deletions, mr.Comments)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM mr_reviews WHERE project_id=? AND iid=?`, projectID, mr.IID); err != nil {
				return err
			}
			for _, r := range mr.Reviews {
				rid, err := resolveAuthor(ctx, tx, projectID, r.AuthorName, "", r.AuthorLogin)
				if err != nil {
					return err
				}
				if rid == aid {
					continue // автор не ревьюит сам себя
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO mr_reviews(project_id, iid, author_id, at, comments) VALUES (?, ?, ?, ?, ?)
					ON CONFLICT(project_id, iid, author_id) DO UPDATE SET comments=comments+excluded.comments, at=MIN(at, excluded.at)`,
					projectID, mr.IID, rid, unix(r.At), r.Comments); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SaveIssues добавляет или обновляет задачи.
func (s *Store) SaveIssues(ctx context.Context, projectID int64, issues []domain.Issue) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, is := range issues {
			var assignee any
			if is.AssigneeLogin != "" || is.AssigneeName != "" {
				id, err := resolveAuthor(ctx, tx, projectID, is.AssigneeName, "", is.AssigneeLogin)
				if err != nil {
					return err
				}
				assignee = id
			}
			labels, _ := json.Marshal(nonNil(is.Labels))
			_, err := tx.ExecContext(ctx, `INSERT INTO issues
				(project_id, iid, title, labels, state, author_login, assignee_id, created_at, closed_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(project_id, iid) DO UPDATE SET title=excluded.title, labels=excluded.labels,
					state=excluded.state, assignee_id=excluded.assignee_id, closed_at=excluded.closed_at`,
				projectID, is.IID, is.Title, string(labels), is.State, is.AuthorLogin, assignee, unix(is.CreatedAt), unixPtr(is.ClosedAt))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// LoadDataset загружает всю активность проекта до момента `to`.
// История до начала периода нужна для владения кодом (bus factor) и rework.
func (s *Store) LoadDataset(ctx context.Context, projectID int64, to time.Time) (*domain.Dataset, error) {
	ds := &domain.Dataset{Authors: map[int64]domain.Author{}}
	authors, err := s.ListAuthors(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for _, a := range authors {
		ds.Authors[a.ID] = a
	}
	if ds.Commits, err = s.loadCommits(ctx, projectID, to); err != nil {
		return nil, err
	}
	if ds.MergeRequests, err = s.loadMRs(ctx, projectID, to); err != nil {
		return nil, err
	}
	if ds.Issues, err = s.loadIssues(ctx, projectID, to); err != nil {
		return nil, err
	}
	return ds, nil
}

func (s *Store) loadCommits(ctx context.Context, projectID int64, to time.Time) ([]domain.Commit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT sha, author_id, authored_at, tz_offset, message, is_merge,
		additions, deletions, files_changed FROM commits WHERE project_id=? AND authored_at < ? ORDER BY authored_at, sha`,
		projectID, unix(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var commits []domain.Commit
	idx := map[string]int{}
	for rows.Next() {
		var c domain.Commit
		var at int64
		if err := rows.Scan(&c.SHA, &c.AuthorID, &at, &c.TZOffset, &c.Message, &c.IsMerge,
			&c.Additions, &c.Deletions, &c.FilesChanged); err != nil {
			return nil, err
		}
		c.ProjectID = projectID
		c.AuthoredAt = fromUnix(at)
		idx[c.SHA] = len(commits)
		commits = append(commits, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	frows, err := s.db.QueryContext(ctx, `SELECT sha, path, additions, deletions, is_test, excluded
		FROM file_changes WHERE project_id=? ORDER BY sha, path`, projectID)
	if err != nil {
		return nil, err
	}
	defer frows.Close()
	for frows.Next() {
		var sha string
		var f domain.FileChange
		if err := frows.Scan(&sha, &f.Path, &f.Additions, &f.Deletions, &f.IsTest, &f.Excluded); err != nil {
			return nil, err
		}
		if i, ok := idx[sha]; ok {
			commits[i].Files = append(commits[i].Files, f)
		}
	}
	return commits, frows.Err()
}

func (s *Store) loadMRs(ctx context.Context, projectID int64, to time.Time) ([]domain.MergeRequest, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT iid, title, author_id, state, created_at, merged_at, closed_at,
		first_review_at, additions, deletions, comments FROM merge_requests WHERE project_id=? AND created_at < ? ORDER BY iid`,
		projectID, unix(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var mrs []domain.MergeRequest
	idx := map[int]int{}
	for rows.Next() {
		var mr domain.MergeRequest
		var created int64
		var merged, closed, review sql.NullInt64
		if err := rows.Scan(&mr.IID, &mr.Title, &mr.AuthorID, &mr.State, &created, &merged, &closed,
			&review, &mr.Additions, &mr.Deletions, &mr.Comments); err != nil {
			return nil, err
		}
		mr.ProjectID = projectID
		mr.CreatedAt = fromUnix(created)
		mr.MergedAt, mr.ClosedAt, mr.FirstReviewAt = fromNull(merged), fromNull(closed), fromNull(review)
		idx[mr.IID] = len(mrs)
		mrs = append(mrs, mr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rrows, err := s.db.QueryContext(ctx, `SELECT iid, author_id, at, comments FROM mr_reviews WHERE project_id=?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rrows.Close()
	for rrows.Next() {
		var iid int
		var r domain.Review
		var at int64
		if err := rrows.Scan(&iid, &r.AuthorID, &at, &r.Comments); err != nil {
			return nil, err
		}
		r.At = fromUnix(at)
		if i, ok := idx[iid]; ok {
			mrs[i].Reviews = append(mrs[i].Reviews, r)
		}
	}
	for i := range mrs {
		sort.Slice(mrs[i].Reviews, func(a, b int) bool { return mrs[i].Reviews[a].At.Before(mrs[i].Reviews[b].At) })
	}
	return mrs, rrows.Err()
}

func (s *Store) loadIssues(ctx context.Context, projectID int64, to time.Time) ([]domain.Issue, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT iid, title, labels, state, author_login, assignee_id, created_at, closed_at
		FROM issues WHERE project_id=? AND created_at < ? ORDER BY iid`, projectID, unix(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Issue
	for rows.Next() {
		var is domain.Issue
		var labels string
		var assignee sql.NullInt64
		var created int64
		var closed sql.NullInt64
		if err := rows.Scan(&is.IID, &is.Title, &labels, &is.State, &is.AuthorLogin, &assignee, &created, &closed); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(labels), &is.Labels)
		is.ProjectID = projectID
		is.AssigneeID = assignee.Int64
		is.CreatedAt = fromUnix(created)
		is.ClosedAt = fromNull(closed)
		out = append(out, is)
	}
	return out, rows.Err()
}

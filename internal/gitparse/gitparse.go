// Package gitparse клонирует репозитории и извлекает из них историю коммитов.
package gitparse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"

	"teampulse/internal/domain"
)

// ErrEmptyRepo — в репозитории ещё нет коммитов.
var ErrEmptyRepo = errors.New("репозиторий пуст")

const maxMessageLen = 4000

// IsLocalPath сообщает, что источник — путь на диске, а не URL.
func IsLocalPath(src string) bool {
	return !strings.Contains(src, "://") || strings.HasPrefix(src, "file://")
}

// Open возвращает репозиторий, готовый к чтению: локальный открывает на месте,
// удалённый клонирует в dir (bare) или обновляет существующий клон через fetch.
func Open(ctx context.Context, src, dir, token string) (*git.Repository, error) {
	if IsLocalPath(src) {
		repo, err := git.PlainOpenWithOptions(strings.TrimPrefix(src, "file://"), &git.PlainOpenOptions{DetectDotGit: true})
		if err != nil {
			return nil, fmt.Errorf("открытие локального репозитория: %w", err)
		}
		return repo, nil
	}
	var auth transport.AuthMethod
	if token != "" {
		auth = &githttp.BasicAuth{Username: "oauth2", Password: token}
	}
	if repo, err := git.PlainOpen(dir); err == nil {
		err = repo.FetchContext(ctx, &git.FetchOptions{Auth: auth, Force: true, Tags: git.NoTags})
		if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
			return nil, describe(err)
		}
		return repo, nil
	}
	_ = os.RemoveAll(dir) // остатки прерванного клонирования
	repo, err := git.PlainCloneContext(ctx, dir, true, &git.CloneOptions{URL: src, Auth: auth, Tags: git.NoTags})
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, describe(err)
	}
	return repo, nil
}

// describe переводит ошибки транспорта в понятные пользователю.
func describe(err error) error {
	switch {
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return ErrEmptyRepo
	case errors.Is(err, transport.ErrAuthenticationRequired), errors.Is(err, transport.ErrAuthorizationFailed):
		return errors.New("доступ к репозиторию запрещён: укажите корректный токен")
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return errors.New("репозиторий не найден (проверьте URL и права токена)")
	case errors.Is(err, context.DeadlineExceeded):
		return errors.New("превышено время клонирования (CLONE_TIMEOUT)")
	}
	return fmt.Errorf("git: %w", err)
}

// ResolveBranch находит коммит-вершину ветки. Пустое имя — ветка по умолчанию.
func ResolveBranch(repo *git.Repository, branch string) (plumbing.Hash, error) {
	if branch == "" {
		head, err := repo.Head()
		if err != nil {
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				return plumbing.ZeroHash, ErrEmptyRepo
			}
			return plumbing.ZeroHash, err
		}
		if !head.Name().IsBranch() { // detached HEAD локального репозитория
			return head.Hash(), nil
		}
		branch = head.Name().Short()
	}
	// В bare-клоне свежие данные после fetch лежат в refs/remotes/origin/*.
	for _, name := range []plumbing.ReferenceName{
		plumbing.NewRemoteReferenceName("origin", branch),
		plumbing.NewBranchReferenceName(branch),
	} {
		if ref, err := repo.Reference(name, true); err == nil {
			return ref.Hash(), nil
		}
	}
	return plumbing.ZeroHash, fmt.Errorf("ветка %q не найдена", branch)
}

// Result — итог разбора истории.
type Result struct {
	Commits   []domain.Commit
	HeadSHA   string
	Truncated bool // достигнут лимит MaxCommits, остальная история догрузится при следующих синхронизациях
	Skipped   int  // коммиты с некорректными датами
}

// ParseOptions — параметры разбора.
type ParseOptions struct {
	Known      map[string]bool // уже загруженные коммиты
	MaxCommits int
	Classifier *Classifier
	Now        time.Time
}

// Parse обходит историю от head и возвращает новые коммиты со статистикой файлов.
func Parse(ctx context.Context, repo *git.Repository, head plumbing.Hash, opt ParseOptions) (Result, error) {
	res := Result{HeadSHA: head.String()}
	if opt.Classifier == nil {
		opt.Classifier = NewClassifier(nil)
	}
	if opt.Now.IsZero() {
		opt.Now = time.Now()
	}
	iter, err := repo.Log(&git.LogOptions{From: head, Order: git.LogOrderCommitterTime})
	if err != nil {
		return res, err
	}
	defer iter.Close()
	err = iter.ForEach(func(c *object.Commit) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		sha := c.Hash.String()
		if opt.Known[sha] {
			return nil
		}
		if opt.MaxCommits > 0 && len(res.Commits) >= opt.MaxCommits {
			res.Truncated = true
			return storer.ErrStop
		}
		when, ok := validTime(c, opt.Now)
		if !ok {
			res.Skipped++
			return nil
		}
		_, offset := when.Zone()
		dc := domain.Commit{
			SHA:         sha,
			AuthorName:  clean(c.Author.Name),
			AuthorEmail: strings.ToLower(clean(c.Author.Email)),
			AuthoredAt:  when.UTC(),
			TZOffset:    offset / 60,
			Message:     truncate(clean(strings.TrimSpace(c.Message)), maxMessageLen),
			IsMerge:     c.NumParents() > 1,
		}
		if dc.AuthorEmail == "" {
			dc.AuthorEmail = strings.ToLower(strings.ReplaceAll(dc.AuthorName, " ", ".")) + "@unknown"
		}
		// У merge-коммитов diff повторяет уже учтённые изменения — не считаем.
		if !dc.IsMerge {
			stats, err := c.StatsContext(ctx)
			if err != nil {
				return fmt.Errorf("статистика коммита %s: %w", sha[:8], err)
			}
			for _, s := range stats {
				f := domain.FileChange{
					Path:      clean(s.Name),
					Additions: s.Addition,
					Deletions: s.Deletion,
					IsTest:    IsTest(s.Name),
					Excluded:  opt.Classifier.Excluded(s.Name),
				}
				dc.Files = append(dc.Files, f)
				if !f.Excluded {
					dc.Additions += f.Additions
					dc.Deletions += f.Deletions
					dc.FilesChanged++
				}
			}
		}
		res.Commits = append(res.Commits, dc)
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		return res, err
	}
	return res, nil
}

// validTime защищает от «сломанных» дат (1970 год, будущее): берём дату автора,
// при её некорректности — дату коммиттера.
func validTime(c *object.Commit, now time.Time) (time.Time, bool) {
	minT := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	maxT := now.Add(48 * time.Hour)
	for _, t := range []time.Time{c.Author.When, c.Committer.When} {
		if t.After(minT) && t.Before(maxT) {
			return t, true
		}
	}
	return time.Time{}, false
}

func clean(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

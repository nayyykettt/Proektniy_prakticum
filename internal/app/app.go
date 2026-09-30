// Package app — прикладной слой: синхронизация данных, анализ и отчёты.
// Используется HTTP API, веб-интерфейсом, планировщиком и CLI.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"teampulse/internal/config"
	"teampulse/internal/domain"
	"teampulse/internal/gitparse"
	"teampulse/internal/metrics"
	"teampulse/internal/provider"
	"teampulse/internal/secure"
	"teampulse/internal/storage"
	"teampulse/internal/zerocracy"
)

// App объединяет зависимости прикладного слоя.
type App struct {
	Cfg    *config.Config
	Store  *storage.Store
	Box    *secure.Box
	Policy zerocracy.Policy
	Log    *slog.Logger

	// NewProvider создаёт клиента платформы; подменяется в тестах.
	NewProvider func(p domain.Project, token string) (provider.Provider, error)

	mu      sync.Mutex
	running map[int64]bool
	wg      sync.WaitGroup
}

// New создаёт прикладной слой.
func New(cfg *config.Config, store *storage.Store, box *secure.Box, pol zerocracy.Policy, log *slog.Logger) *App {
	a := &App{Cfg: cfg, Store: store, Box: box, Policy: pol, Log: log, running: map[int64]bool{}}
	httpClient := &http.Client{Timeout: 60 * time.Second}
	a.NewProvider = func(p domain.Project, token string) (provider.Provider, error) {
		if p.Provider != domain.ProviderT1Code {
			return nil, nil
		}
		return provider.NewT1Code(p.ProviderURL, p.ProviderProjectID, token, httpClient)
	}
	return a
}

// Wait дожидается завершения фоновых синхронизаций (graceful shutdown).
func (a *App) Wait() { a.wg.Wait() }

// ProjectInput — данные формы/запроса создания и изменения проекта.
type ProjectInput struct {
	Name              string `json:"name"`
	RepoURL           string `json:"repo_url"`
	Branch            string `json:"branch"`
	Provider          string `json:"provider"`
	ProviderURL       string `json:"provider_url"`
	ProviderProjectID string `json:"provider_project_id"`
	Token             string `json:"token"`       // пусто при изменении — оставить прежний
	ClearToken        bool   `json:"clear_token"` // удалить сохранённый токен
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrInvalid, fmt.Sprintf(format, args...))
}

// validate нормализует и проверяет ввод; защищает от SSRF и некорректных данных.
func (a *App) validate(ctx context.Context, in *ProjectInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.RepoURL = strings.TrimSpace(in.RepoURL)
	in.Branch = strings.TrimSpace(in.Branch)
	in.Provider = strings.TrimSpace(in.Provider)
	in.ProviderURL = strings.TrimSpace(in.ProviderURL)
	in.ProviderProjectID = strings.TrimSpace(in.ProviderProjectID)
	in.Token = strings.TrimSpace(in.Token)

	if n := utf8.RuneCountInString(in.Name); n == 0 || n > 100 {
		return invalid("название проекта: от 1 до 100 символов")
	}
	if in.RepoURL == "" {
		return invalid("укажите адрес репозитория")
	}
	if len(in.Branch) > 200 || strings.ContainsAny(in.Branch, " ~^:?*[\\") || strings.Contains(in.Branch, "..") {
		return invalid("некорректное имя ветки")
	}
	if gitparse.IsLocalPath(in.RepoURL) {
		if !a.Cfg.AllowLocalRepos {
			return invalid("локальные пути отключены (ALLOW_LOCAL_REPOS=false) — укажите https-адрес")
		}
		if _, err := os.Stat(strings.TrimPrefix(in.RepoURL, "file://")); err != nil {
			return invalid("локальный путь не найден")
		}
	} else if _, err := secure.ValidateRemoteURL(ctx, in.RepoURL, a.Cfg.AllowLocalRepos); err != nil {
		return invalid("адрес репозитория: %v", err)
	}
	switch in.Provider {
	case "", domain.ProviderNone:
		in.Provider = domain.ProviderNone
		in.ProviderURL, in.ProviderProjectID = "", ""
	case domain.ProviderT1Code:
		if in.ProviderProjectID == "" || in.ProviderURL == "" {
			return invalid("для Т1 Код укажите адрес инстанса и ID/путь проекта")
		}
		if _, err := secure.ValidateRemoteURL(ctx, in.ProviderURL, a.Cfg.AllowLocalRepos); err != nil {
			return invalid("адрес Т1 Код: %v", err)
		}
	default:
		return invalid("неизвестный провайдер %q", in.Provider)
	}
	if len(in.Token) > 500 {
		return invalid("слишком длинный токен")
	}
	return nil
}

// CreateProject создаёт проект пользователя ownerID (0 — без владельца, например по
// сервисному API-токену); токен сохраняется только в зашифрованном виде.
func (a *App) CreateProject(ctx context.Context, ownerID int64, in ProjectInput) (domain.Project, error) {
	if err := a.validate(ctx, &in); err != nil {
		return domain.Project{}, err
	}
	enc, err := a.Box.Encrypt(in.Token)
	if err != nil {
		return domain.Project{}, err
	}
	p := domain.Project{OwnerID: ownerID, Name: in.Name, RepoURL: in.RepoURL, Branch: in.Branch, Provider: in.Provider,
		ProviderURL: in.ProviderURL, ProviderProjectID: in.ProviderProjectID, TokenEnc: enc}
	if err := a.Store.CreateProject(ctx, &p); err != nil {
		return domain.Project{}, err
	}
	a.Log.Info("проект создан", "project", p.ID, "name", p.Name, "owner", ownerID)
	return p, nil
}

// UpdateProject изменяет настройки проекта. Смена репозитория сбрасывает клон.
func (a *App) UpdateProject(ctx context.Context, id int64, in ProjectInput) (domain.Project, error) {
	p, err := a.Store.GetProject(ctx, id)
	if err != nil {
		return p, err
	}
	if err := a.validate(ctx, &in); err != nil {
		return p, err
	}
	switch {
	case in.ClearToken:
		p.TokenEnc = nil
	case in.Token != "":
		if p.TokenEnc, err = a.Box.Encrypt(in.Token); err != nil {
			return p, err
		}
	}
	repoChanged := p.RepoURL != in.RepoURL
	p.Name, p.RepoURL, p.Branch, p.Provider = in.Name, in.RepoURL, in.Branch, in.Provider
	p.ProviderURL, p.ProviderProjectID = in.ProviderURL, in.ProviderProjectID
	if err := a.Store.UpdateProject(ctx, &p); err != nil {
		return p, err
	}
	if repoChanged {
		_ = os.RemoveAll(a.repoDir(id))
	}
	p.HasToken = len(p.TokenEnc) > 0
	return p, nil
}

// DeleteProject удаляет проект, его данные и клон.
func (a *App) DeleteProject(ctx context.Context, id int64) error {
	if a.isRunning(id) {
		return domain.ErrSyncActive
	}
	if err := a.Store.DeleteProject(ctx, id); err != nil {
		return err
	}
	return os.RemoveAll(a.repoDir(id))
}

func (a *App) repoDir(id int64) string {
	return filepath.Join(a.Cfg.ReposDir, fmt.Sprint(id))
}

func (a *App) isRunning(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running[id]
}

func (a *App) acquire(id int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running[id] {
		return false
	}
	a.running[id] = true
	return true
}

func (a *App) release(id int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.running, id)
}

// IsRunning сообщает, идёт ли синхронизация проекта.
func (a *App) IsRunning(id int64) bool { return a.isRunning(id) }

// SyncResult — итог синхронизации.
type SyncResult struct {
	NewCommits    int      `json:"new_commits"`
	Skipped       int      `json:"skipped_commits"`
	Truncated     bool     `json:"truncated"`
	MergeRequests int      `json:"merge_requests"`
	Issues        int      `json:"issues"`
	Assigned      int      `json:"assigned"`
	Warnings      []string `json:"warnings,omitempty"`
	Status        string   `json:"status"`
	Duration      string   `json:"duration"`
}

// SyncAsync запускает синхронизацию в фоне.
func (a *App) SyncAsync(id int64) error {
	if _, err := a.Store.GetProject(context.Background(), id); err != nil {
		return err
	}
	if !a.acquire(id) {
		return domain.ErrSyncActive
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		defer a.release(id)
		if _, err := a.syncLocked(context.Background(), id); err != nil {
			a.Log.Warn("синхронизация завершилась с ошибкой", "project", id, "err", err)
		}
	}()
	return nil
}

// Sync синхронизирует проект и ждёт результата.
func (a *App) Sync(ctx context.Context, id int64) (SyncResult, error) {
	if !a.acquire(id) {
		return SyncResult{}, domain.ErrSyncActive
	}
	defer a.release(id)
	return a.syncLocked(ctx, id)
}

func (a *App) syncLocked(ctx context.Context, id int64) (res SyncResult, err error) {
	start := time.Now()
	p, err := a.Store.GetProject(ctx, id)
	if err != nil {
		return res, err
	}
	_ = a.Store.SetSyncState(ctx, id, domain.SyncRunning, "", "")
	log := a.Log.With("project", id)
	log.Info("синхронизация начата")

	head, gitErr := a.syncGit(ctx, p, &res)
	if gitErr != nil {
		res.Status = domain.SyncFailed
		_ = a.Store.SetSyncState(context.WithoutCancel(ctx), id, domain.SyncFailed, gitErr.Error(), "")
		log.Warn("ошибка Git", "err", gitErr)
		return res, gitErr
	}

	res.Status = domain.SyncOK
	var syncErr string
	if provErr := a.syncProvider(ctx, p, &res); provErr != nil {
		res.Status = domain.SyncPartial
		syncErr = provErr.Error()
		res.Warnings = append(res.Warnings, syncErr)
		log.Warn("ошибка Т1 Код", "err", provErr)
	}
	if res.Truncated {
		res.Warnings = append(res.Warnings, fmt.Sprintf("загружено %d коммитов (лимит MAX_COMMITS), остальная история догрузится при следующих синхронизациях", res.NewCommits))
	}
	if res.Skipped > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("пропущено коммитов с некорректной датой: %d", res.Skipped))
	}
	if syncErr == "" && len(res.Warnings) > 0 {
		syncErr = strings.Join(res.Warnings, "; ")
	}
	_ = a.Store.SetSyncState(context.WithoutCancel(ctx), id, res.Status, syncErr, head)
	res.Duration = time.Since(start).Round(time.Millisecond).String()
	log.Info("синхронизация завершена", "status", res.Status, "commits", res.NewCommits, "mrs", res.MergeRequests,
		"issues", res.Issues, "duration", res.Duration)
	return res, nil
}

func (a *App) syncGit(ctx context.Context, p domain.Project, res *SyncResult) (string, error) {
	token, err := a.Box.Decrypt(p.TokenEnc)
	if err != nil {
		return "", err
	}
	gctx, cancel := context.WithTimeout(ctx, a.Cfg.CloneTimeout)
	defer cancel()
	repo, err := gitparse.Open(gctx, p.RepoURL, a.repoDir(p.ID), token)
	if errors.Is(err, gitparse.ErrEmptyRepo) {
		res.Warnings = append(res.Warnings, "репозиторий пуст")
		return "", nil
	}
	if err != nil {
		return "", err
	}
	head, err := gitparse.ResolveBranch(repo, p.Branch)
	if errors.Is(err, gitparse.ErrEmptyRepo) {
		res.Warnings = append(res.Warnings, "репозиторий пуст")
		return "", nil
	}
	if err != nil {
		return "", err
	}
	known, err := a.Store.KnownSHAs(ctx, p.ID)
	if err != nil {
		return "", err
	}
	parsed, err := gitparse.Parse(gctx, repo, head, gitparse.ParseOptions{
		Known: known, MaxCommits: a.Cfg.MaxCommits, Classifier: gitparse.NewClassifier(a.Cfg.ExcludePatterns),
	})
	if err != nil {
		return "", fmt.Errorf("разбор истории: %w", err)
	}
	if err := a.Store.SaveCommits(ctx, p.ID, parsed.Commits); err != nil {
		return "", err
	}
	res.NewCommits, res.Skipped, res.Truncated = len(parsed.Commits), parsed.Skipped, parsed.Truncated
	return parsed.HeadSHA, nil
}

func (a *App) syncProvider(ctx context.Context, p domain.Project, res *SyncResult) error {
	if p.Provider != domain.ProviderT1Code {
		return nil
	}
	token, err := a.Box.Decrypt(p.TokenEnc)
	if err != nil {
		return err
	}
	prov, err := a.NewProvider(p, token)
	if err != nil || prov == nil {
		return err
	}
	// Инкрементально: всё, что менялось с прошлой успешной синхронизации (с запасом в сутки).
	var since time.Time
	if p.LastSyncAt != nil && p.SyncStatus == domain.SyncOK {
		since = p.LastSyncAt.Add(-24 * time.Hour)
	}
	mrs, err := prov.FetchMergeRequests(ctx, since)
	if err != nil {
		return err
	}
	if err := a.Store.SaveMergeRequests(ctx, p.ID, mrs); err != nil {
		return err
	}
	issues, err := prov.FetchIssues(ctx, since)
	if err != nil {
		return err
	}
	if err := a.Store.SaveIssues(ctx, p.ID, issues); err != nil {
		return err
	}
	res.MergeRequests, res.Issues = len(mrs), len(issues)
	if a.Cfg.AutoAssign {
		res.Assigned = a.autoAssign(ctx, p, prov)
	}
	return nil
}

// autoAssign назначает неназначенные задачи лучшему кандидату (Zerocracy).
func (a *App) autoAssign(ctx context.Context, p domain.Project, prov provider.Provider) int {
	ds, err := a.Store.LoadDataset(ctx, p.ID, time.Now().Add(time.Hour))
	if err != nil {
		return 0
	}
	ds = metrics.WithoutBots(ds)
	assigned := 0
	for _, s := range zerocracy.Suggest(ds, time.Now(), nil, 20) {
		if len(s.Candidates) == 0 || s.Candidates[0].Login == "" || s.Candidates[0].Expertise == 0 {
			continue
		}
		c := s.Candidates[0]
		if err := prov.AssignIssue(ctx, s.IssueIID, c.Login); err != nil {
			a.Log.Warn("автоназначение не удалось", "project", p.ID, "issue", s.IssueIID, "err", err)
			continue
		}
		a.Log.Info("задача назначена автоматически", "project", p.ID, "issue", s.IssueIID, "login", c.Login)
		assigned++
	}
	return assigned
}

// MetricOptions — настройки расчёта метрик из конфигурации.
func (a *App) MetricOptions() metrics.Options {
	o := metrics.DefaultOptions()
	o.WorkdayStart, o.WorkdayEnd = a.Cfg.WorkdayStart, a.Cfg.WorkdayEnd
	return o
}

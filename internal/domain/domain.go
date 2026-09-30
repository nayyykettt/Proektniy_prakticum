// Package domain содержит модели предметной области, общие для всех слоёв.
package domain

import (
	"errors"
	"time"
)

// Ошибки, которые слои выше превращают в понятные HTTP-ответы.
var (
	ErrNotFound   = errors.New("не найдено")
	ErrInvalid    = errors.New("некорректные данные")
	ErrConflict   = errors.New("конфликт")
	ErrSyncActive = errors.New("синхронизация уже выполняется")
)

// Провайдеры данных о задачах и merge request'ах.
const (
	ProviderNone   = "none"   // только Git
	ProviderT1Code = "t1code" // Т1 Код / Сфера.Код (GitLab-совместимый API)
)

// Статусы синхронизации проекта.
const (
	SyncNever   = "never"
	SyncRunning = "running"
	SyncOK      = "ok"
	SyncPartial = "partial" // Git загружен, провайдер вернул ошибку
	SyncFailed  = "failed"
)

// Project — анализируемый репозиторий.
type Project struct {
	ID                int64      `json:"id"`
	OwnerID           int64      `json:"-"` // владелец; 0 — проект без владельца (создан до учётных записей или по API-токену)
	Name              string     `json:"name"`
	RepoURL           string     `json:"repo_url"`
	Branch            string     `json:"branch"`
	Provider          string     `json:"provider"`
	ProviderURL       string     `json:"provider_url,omitempty"`
	ProviderProjectID string     `json:"provider_project_id,omitempty"`
	TokenEnc          []byte     `json:"-"` // никогда не сериализуется
	HasToken          bool       `json:"has_token"`
	LastSyncedSHA     string     `json:"last_synced_sha,omitempty"`
	SyncStatus        string     `json:"sync_status"`
	SyncError         string     `json:"sync_error,omitempty"`
	LastSyncAt        *time.Time `json:"last_sync_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
}

// User — учётная запись. Пользователь видит и изменяет только свои проекты.
type User struct {
	ID        int64     `json:"id"`
	Login     string    `json:"login"`
	CreatedAt time.Time `json:"created_at"`
}

// Author — разработчик (каноническая идентичность после слияния алиасов).
type Author struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Login string `json:"login,omitempty"`
}

// FileChange — изменение одного файла в коммите.
type FileChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	IsTest    bool   `json:"is_test"`
	Excluded  bool   `json:"excluded"` // vendor, lock-файлы, сгенерированный код
}

// Commit — коммит; Additions/Deletions считаются только по неисключённым файлам.
type Commit struct {
	SHA          string       `json:"sha"`
	ProjectID    int64        `json:"-"`
	AuthorID     int64        `json:"author_id"`
	AuthorName   string       `json:"author_name"`
	AuthorEmail  string       `json:"author_email"`
	AuthoredAt   time.Time    `json:"authored_at"` // UTC
	TZOffset     int          `json:"tz_offset"`   // смещение автора в минутах
	Message      string       `json:"message"`
	IsMerge      bool         `json:"is_merge"`
	Additions    int          `json:"additions"`
	Deletions    int          `json:"deletions"`
	FilesChanged int          `json:"files_changed"`
	Files        []FileChange `json:"files,omitempty"`
}

// LocalTime возвращает время коммита в часовом поясе автора.
func (c Commit) LocalTime() time.Time {
	return c.AuthoredAt.In(time.FixedZone("", c.TZOffset*60))
}

// MergeRequest — merge request из Т1 Код.
type MergeRequest struct {
	ProjectID     int64      `json:"-"`
	IID           int        `json:"iid"`
	Title         string     `json:"title"`
	AuthorID      int64      `json:"author_id"`
	AuthorLogin   string     `json:"author_login"`
	AuthorName    string     `json:"author_name"`
	State         string     `json:"state"` // opened | merged | closed
	CreatedAt     time.Time  `json:"created_at"`
	MergedAt      *time.Time `json:"merged_at,omitempty"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
	FirstReviewAt *time.Time `json:"first_review_at,omitempty"`
	Additions     int        `json:"additions"` // -1, если размер неизвестен
	Deletions     int        `json:"deletions"`
	Comments      int        `json:"comments"`
	Reviews       []Review   `json:"reviews,omitempty"`
}

// Review — участие разработчика в ревью MR (комментарии или approve).
type Review struct {
	AuthorID    int64     `json:"author_id"`
	AuthorLogin string    `json:"author_login"`
	AuthorName  string    `json:"author_name"`
	At          time.Time `json:"at"`
	Comments    int       `json:"comments"`
}

// Issue — задача из Т1 Код.
type Issue struct {
	ProjectID     int64      `json:"-"`
	IID           int        `json:"iid"`
	Title         string     `json:"title"`
	Labels        []string   `json:"labels"`
	State         string     `json:"state"` // opened | closed
	AuthorLogin   string     `json:"author_login"`
	AssigneeID    int64      `json:"assignee_id,omitempty"`
	AssigneeLogin string     `json:"assignee_login,omitempty"`
	AssigneeName  string     `json:"assignee_name,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	ClosedAt      *time.Time `json:"closed_at,omitempty"`
}

// Team — именованная группа разработчиков внутри проекта.
type Team struct {
	ID        int64   `json:"id"`
	ProjectID int64   `json:"project_id"`
	Name      string  `json:"name"`
	MemberIDs []int64 `json:"member_ids"`
}

// StoredReport — сохранённый отчёт; Payload — JSON с данными отчёта.
type StoredReport struct {
	ID          int64     `json:"id"`
	ProjectID   int64     `json:"project_id"`
	TeamID      int64     `json:"team_id,omitempty"`
	From        time.Time `json:"from"`
	To          time.Time `json:"to"`
	GeneratedAt time.Time `json:"generated_at"`
	Trigger     string    `json:"trigger"` // manual | schedule
	Payload     []byte    `json:"-"`
}

// Period — полуинтервал [From, To).
type Period struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Validate проверяет корректность периода.
func (p Period) Validate() error {
	if p.From.IsZero() || p.To.IsZero() || !p.From.Before(p.To) {
		return errors.New("период: дата начала должна быть раньше даты конца")
	}
	if p.To.Sub(p.From) > 5*366*24*time.Hour {
		return errors.New("период: не более 5 лет")
	}
	return nil
}

// Contains сообщает, входит ли момент t в период.
func (p Period) Contains(t time.Time) bool {
	return !t.Before(p.From) && t.Before(p.To)
}

// Previous — предыдущий период той же длины (для трендов).
func (p Period) Previous() Period {
	d := p.To.Sub(p.From)
	return Period{From: p.From.Add(-d), To: p.From}
}

// Dataset — все данные проекта, нужные для расчёта метрик.
type Dataset struct {
	Authors       map[int64]Author
	Commits       []Commit // по возрастанию AuthoredAt
	MergeRequests []MergeRequest
	Issues        []Issue
}

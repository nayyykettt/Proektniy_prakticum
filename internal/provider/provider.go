// Package provider — интеграция с платформами хостинга кода (Т1 Код / Сфера.Код).
package provider

import (
	"context"
	"time"

	"teampulse/internal/domain"
)

// Provider — источник данных о merge request'ах и задачах.
// Реализация для другой платформы подключается без изменения остального кода.
type Provider interface {
	// FetchMergeRequests возвращает MR, изменённые после since (нулевое время — все).
	FetchMergeRequests(ctx context.Context, since time.Time) ([]domain.MergeRequest, error)
	// FetchIssues возвращает задачи, изменённые после since.
	FetchIssues(ctx context.Context, since time.Time) ([]domain.Issue, error)
	// AssignIssue назначает задачу пользователю по логину.
	AssignIssue(ctx context.Context, iid int, login string) error
}

// Package scheduler обеспечивает работу «без ручного вмешательства»:
// периодическую синхронизацию проектов и автоматическую генерацию отчётов.
package scheduler

import (
	"context"
	"errors"
	"time"

	"teampulse/internal/app"
	"teampulse/internal/domain"
)

// TriggerSchedule — отметка автоматически созданных отчётов.
const TriggerSchedule = "schedule"

// Run проверяет проекты раз в tick, пока не отменён ctx.
func Run(ctx context.Context, a *app.App, tick time.Duration) {
	if a.Cfg.SyncInterval == 0 && a.Cfg.ReportInterval == 0 {
		a.Log.Info("планировщик выключен")
		return
	}
	a.Log.Info("планировщик запущен", "sync_interval", a.Cfg.SyncInterval, "report_interval", a.Cfg.ReportInterval)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		RunOnce(ctx, a, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunOnce выполняет один проход: синхронизирует устаревшие проекты и формирует просроченные отчёты.
func RunOnce(ctx context.Context, a *app.App, now time.Time) {
	projects, err := a.Store.ListProjects(ctx)
	if err != nil {
		a.Log.Error("планировщик: список проектов", "err", err)
		return
	}
	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		if due(p.LastSyncAt, a.Cfg.SyncInterval, now) {
			if _, err := a.Sync(ctx, p.ID); err != nil && !errors.Is(err, domain.ErrSyncActive) {
				a.Log.Warn("планировщик: синхронизация", "project", p.ID, "err", err)
			}
			if p, err = a.Store.GetProject(ctx, p.ID); err != nil {
				continue
			}
		}
		// Отчёт имеет смысл только по успешно загруженным данным.
		if a.Cfg.ReportInterval == 0 || (p.SyncStatus != domain.SyncOK && p.SyncStatus != domain.SyncPartial) {
			continue
		}
		last, err := a.Store.LastReportAt(ctx, p.ID, TriggerSchedule)
		if err != nil {
			continue
		}
		var lastPtr *time.Time
		if !last.IsZero() {
			lastPtr = &last
		}
		if due(lastPtr, a.Cfg.ReportInterval, now) {
			period := domain.Period{From: now.Add(-a.Cfg.ReportInterval).UTC(), To: now.UTC()}
			if _, err := a.GenerateReport(ctx, p.ID, period, 0, TriggerSchedule); err != nil {
				a.Log.Warn("планировщик: отчёт", "project", p.ID, "err", err)
			}
		}
	}
}

func due(last *time.Time, interval time.Duration, now time.Time) bool {
	if interval == 0 {
		return false
	}
	return last == nil || now.Sub(*last) >= interval
}

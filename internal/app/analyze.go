package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"teampulse/internal/analysis"
	"teampulse/internal/domain"
	"teampulse/internal/metrics"
	"teampulse/internal/report"
	"teampulse/internal/zerocracy"
)

// Scope — срез анализа: весь проект, команда или один разработчик.
type Scope struct {
	TeamID   int64
	AuthorID int64
}

// Analyze рассчитывает метрики, тренды, рекомендации и вклад за период.
func (a *App) Analyze(ctx context.Context, projectID int64, period domain.Period, scope Scope) (*report.Data, error) {
	if err := period.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	p, err := a.Store.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	filter, label, err := a.resolveScope(ctx, projectID, scope)
	if err != nil {
		return nil, err
	}
	ds, err := a.Store.LoadDataset(ctx, projectID, period.To)
	if err != nil {
		return nil, err
	}
	ds = metrics.WithoutBots(ds) // активность ботов (dependabot, renovate, CI) не относится к работе команды
	opt := a.MetricOptions()
	snap := metrics.Compute(ds, period, filter, opt)
	prev := metrics.Compute(ds, period.Previous(), filter, opt)
	trends := analysis.Trends(snap.Summary, prev.Summary)
	d := &report.Data{
		Project:         p,
		Scope:           label,
		Period:          period,
		GeneratedAt:     time.Now().UTC(),
		Snapshot:        snap,
		Trends:          trends,
		Recommendations: analysis.Recommend(snap, trends),
		Contribution:    zerocracy.Evaluate(ds, period, filter, a.Policy),
	}
	if scope.AuthorID == 0 {
		d.Suggestions = zerocracy.Suggest(ds, period.To, filter, 20)
	} else {
		// Bus factor описывает команду: для одного человека он всегда 1 и вводит в заблуждение.
		recs := d.Recommendations[:0]
		for _, r := range d.Recommendations {
			if r.Metric != "bus_factor" {
				recs = append(recs, r)
			}
		}
		d.Recommendations = recs
	}
	return d, nil
}

func (a *App) resolveScope(ctx context.Context, projectID int64, s Scope) (metrics.Filter, string, error) {
	switch {
	case s.AuthorID != 0:
		authors, err := a.Store.ListAuthors(ctx, projectID)
		if err != nil {
			return nil, "", err
		}
		for _, au := range authors {
			if au.ID == s.AuthorID {
				return metrics.Filter{au.ID: true}, "Разработчик: " + au.Name, nil
			}
		}
		return nil, "", domain.ErrNotFound
	case s.TeamID != 0:
		t, err := a.Store.GetTeam(ctx, projectID, s.TeamID)
		if err != nil {
			return nil, "", err
		}
		f := metrics.Filter{}
		for _, id := range t.MemberIDs {
			f[id] = true
		}
		return f, "Команда: " + t.Name, nil
	}
	return nil, "Весь проект", nil
}

// GenerateReport формирует и сохраняет отчёт; формат выбирается при выгрузке.
func (a *App) GenerateReport(ctx context.Context, projectID int64, period domain.Period, teamID int64, trigger string) (domain.StoredReport, error) {
	d, err := a.Analyze(ctx, projectID, period, Scope{TeamID: teamID})
	if err != nil {
		return domain.StoredReport{}, err
	}
	payload, err := json.Marshal(d)
	if err != nil {
		return domain.StoredReport{}, err
	}
	r := domain.StoredReport{ProjectID: projectID, TeamID: teamID, From: period.From, To: period.To, Trigger: trigger, Payload: payload}
	if err := a.Store.SaveReport(ctx, &r); err != nil {
		return r, err
	}
	a.Log.Info("отчёт сформирован", "project", projectID, "report", r.ID, "trigger", trigger)
	return r, nil
}

// LoadReport читает сохранённый отчёт.
func (a *App) LoadReport(ctx context.Context, id int64) (*report.Data, domain.StoredReport, error) {
	r, err := a.Store.GetReport(ctx, id)
	if err != nil {
		return nil, r, err
	}
	var d report.Data
	if err := json.Unmarshal(r.Payload, &d); err != nil {
		return nil, r, fmt.Errorf("отчёт %d повреждён: %w", id, err)
	}
	return &d, r, nil
}

// DefaultPeriod — последние days дней, включая сегодняшний (UTC).
func DefaultPeriod(days int, now time.Time) domain.Period {
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	return domain.Period{From: end.AddDate(0, 0, -days), To: end}
}

// ParsePeriod разбирает даты YYYY-MM-DD (to — включительно).
func ParsePeriod(from, to string, now time.Time) (domain.Period, error) {
	if from == "" && to == "" {
		return DefaultPeriod(30, now), nil
	}
	p := DefaultPeriod(30, now)
	var err error
	if to != "" {
		t, e := time.Parse("2006-01-02", to)
		if e != nil {
			return p, fmt.Errorf("%w: дата конца должна быть в формате ГГГГ-ММ-ДД", domain.ErrInvalid)
		}
		p.To = t.AddDate(0, 0, 1)
		p.From = p.To.AddDate(0, 0, -30)
	}
	if from != "" {
		p.From, err = time.Parse("2006-01-02", from)
		if err != nil {
			return p, fmt.Errorf("%w: дата начала должна быть в формате ГГГГ-ММ-ДД", domain.ErrInvalid)
		}
	}
	if err := p.Validate(); err != nil {
		return p, fmt.Errorf("%w: %v", domain.ErrInvalid, err)
	}
	return p, nil
}

package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"teampulse/internal/analysis"
	"teampulse/internal/domain"
	"teampulse/internal/metrics"
	"teampulse/internal/zerocracy"
)

func sample() *Data {
	t0 := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	merged := t0.Add(30 * time.Hour)
	ds := &domain.Dataset{
		Authors: map[int64]domain.Author{1: {ID: 1, Name: "Иван <script>"}},
		Commits: []domain.Commit{{SHA: "abcdef123", AuthorID: 1, AuthoredAt: t0.Add(10 * time.Hour), Message: "feat: x #1",
			Additions: 5, Files: []domain.FileChange{{Path: "a.go", Additions: 5}}}},
		MergeRequests: []domain.MergeRequest{{IID: 1, AuthorID: 1, CreatedAt: t0, MergedAt: &merged, Additions: 5}},
	}
	p := domain.Period{From: t0, To: t0.AddDate(0, 0, 14)}
	snap := metrics.Compute(ds, p, nil, metrics.DefaultOptions())
	prev := metrics.Compute(ds, p.Previous(), nil, metrics.DefaultOptions())
	tr := analysis.Trends(snap.Summary, prev.Summary)
	return &Data{
		Project: domain.Project{Name: "demo"}, Scope: "Весь проект", Period: p, GeneratedAt: t0,
		Snapshot: snap, Trends: tr, Recommendations: analysis.Recommend(snap, tr),
		Contribution: zerocracy.Evaluate(ds, p, nil, zerocracy.DefaultPolicy()),
	}
}

func TestRenderAllFormats(t *testing.T) {
	d := sample()
	for format := range Formats {
		b, err := Bytes(d, format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		s := string(b)
		if !strings.Contains(s, "demo") {
			t.Errorf("%s: нет имени проекта", format)
		}
		if format != "json" && !strings.Contains(s, "Коммиты") {
			t.Errorf("%s: нет таблицы метрик", format)
		}
	}
	html, _ := Bytes(d, "html")
	if strings.Contains(string(html), "<script>") {
		t.Error("HTML-отчёт должен экранировать пользовательские данные")
	}
	var back Data
	js, _ := Bytes(d, "json")
	if err := json.Unmarshal(js, &back); err != nil || back.Snapshot.Summary.Commits != 1 {
		t.Fatalf("JSON должен читаться обратно: %v", err)
	}
	csv, _ := Bytes(d, "csv")
	if !strings.HasPrefix(string(csv), "\xef\xbb\xbf") {
		t.Error("CSV должен начинаться с BOM для Excel")
	}
	if _, err := Bytes(d, "pdf"); err == nil {
		t.Error("неизвестный формат должен давать ошибку")
	}
	if got := Filename(d, "md"); got != "teampulse_demo_20260302_20260315.md" {
		t.Errorf("имя файла: %s", got)
	}
}

func TestFormatting(t *testing.T) {
	v := func(f float64) *float64 { return &f }
	if Num(nil) != "—" || Num(v(3)) != "3" || Num(v(2.25)) != "2.2" && Num(v(2.25)) != "2.3" {
		t.Errorf("Num: %q %q", Num(v(3)), Num(v(2.25)))
	}
	if Hours(v(5)) != "5.0 ч" || Hours(v(72)) != "3 дн" {
		t.Errorf("Hours: %q %q", Hours(v(5)), Hours(v(72)))
	}
}

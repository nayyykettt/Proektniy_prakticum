// Package report формирует отчёты о производительности в форматах HTML, Markdown, CSV и JSON.
package report

import (
	"bytes"
	"embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	htmltpl "html/template"
	"io"
	"math"
	"strings"
	texttpl "text/template"
	"time"

	"teampulse/internal/analysis"
	"teampulse/internal/domain"
	"teampulse/internal/metrics"
	"teampulse/internal/zerocracy"
)

// Data — всё содержимое отчёта. Сохраняется в БД как JSON, поэтому
// старый отчёт можно перевыгрузить в любом формате без пересчёта.
type Data struct {
	Project         domain.Project            `json:"project"`
	Scope           string                    `json:"scope"` // «Весь проект», «Команда Backend», «Разработчик Иван»
	Period          domain.Period             `json:"period"`
	GeneratedAt     time.Time                 `json:"generated_at"`
	Snapshot        metrics.Snapshot          `json:"snapshot"`
	Trends          []analysis.Trend          `json:"trends"`
	Recommendations []analysis.Recommendation `json:"recommendations"`
	Contribution    zerocracy.Contribution    `json:"contribution"`
	Suggestions     []zerocracy.Suggestion    `json:"suggestions"`
}

// Formats — поддерживаемые форматы и их MIME-типы.
var Formats = map[string]string{
	"html": "text/html; charset=utf-8",
	"md":   "text/markdown; charset=utf-8",
	"csv":  "text/csv; charset=utf-8",
	"json": "application/json",
}

//go:embed templates/*
var tplFS embed.FS

var funcs = map[string]any{
	"num":   Num,
	"hours": Hours,
	"pct":   Pct,
	"date":  func(t time.Time) string { return t.Format("02.01.2006") },
	"datetime": func(t time.Time) string {
		return t.Format("02.01.2006 15:04") + " UTC"
	},
	"lastDay":  func(t time.Time) string { return t.Add(-time.Second).Format("02.01.2006") },
	"verdict":  VerdictText,
	"severity": SeverityText,
	"kind":     func(k string) string { return zerocracy.KindTitles[k] },
	"signed":   func(v int) string { return fmt.Sprintf("%+d", v) },
	"delta":    Delta,
	"bars":     bars,
	"add":      func(a, b int) int { return a + b },
}

var (
	htmlTpl = htmltpl.Must(htmltpl.New("report.html").Funcs(funcs).ParseFS(tplFS, "templates/report.html"))
	mdTpl   = texttpl.Must(texttpl.New("report.md").Funcs(funcs).ParseFS(tplFS, "templates/report.md"))
)

// Render выводит отчёт в нужном формате.
func Render(w io.Writer, d *Data, format string) error {
	switch format {
	case "html":
		return htmlTpl.Execute(w, d)
	case "md":
		return mdTpl.Execute(w, d)
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(d)
	case "csv":
		return renderCSV(w, d)
	}
	return fmt.Errorf("%w: неизвестный формат %q", domain.ErrInvalid, format)
}

// Filename — имя файла для скачивания.
func Filename(d *Data, format string) string {
	name := strings.Map(func(r rune) rune {
		if r == ' ' || r == '/' || r == '\\' || r == '"' {
			return '_'
		}
		return r
	}, d.Project.Name)
	return fmt.Sprintf("teampulse_%s_%s_%s.%s", name, d.Period.From.Format("20060102"),
		d.Period.To.Add(-time.Second).Format("20060102"), format)
}

func renderCSV(w io.Writer, d *Data) error {
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM — Excel корректно покажет кириллицу
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	rows := [][]string{
		{"Отчёт TeamPulse", d.Project.Name, d.Scope, d.Period.From.Format("2006-01-02"), d.Period.To.Add(-time.Second).Format("2006-01-02")},
		{},
		{"Метрика", "Значение", "Ед.", "Прошлый период", "Изменение, %", "Оценка"},
	}
	for _, t := range d.Trends {
		rows = append(rows, []string{t.Title, Num(t.Current), t.Unit, Num(t.Previous), Num(t.DeltaPct), VerdictText(t.Verdict)})
	}
	rows = append(rows, []string{}, []string{"Разработчик", "Коммиты", "Активные дни", "Строк +", "Строк -",
		"Медиана коммита", "Коммиты с тестами, %", "Качество сообщений, %", "MR влито", "Lead time MR, ч",
		"Ревью", "Задачи закрыты", "Файлов во владении", "Баллы Zerocracy"})
	points := map[int64]int{}
	for _, s := range d.Contribution.Scores {
		points[s.AuthorID] = s.Points
	}
	for _, a := range d.Snapshot.Authors {
		rows = append(rows, []string{a.Name, itoa(a.Commits), itoa(a.ActiveDays), itoa(a.Additions), itoa(a.Deletions),
			Num(a.MedianCommitSize), Num(a.TestRatio), Num(a.MessageQuality), itoa(a.MRMerged), Num(a.MRLeadTimeMedianH),
			itoa(a.ReviewsGiven), itoa(a.IssuesClosed), itoa(a.FilesOwned), itoa(points[a.AuthorID])})
	}
	rows = append(rows, []string{}, []string{"Неделя", "Коммиты", "Активные авторы", "MR влито", "Задачи закрыты"})
	for _, wp := range d.Snapshot.Weekly {
		rows = append(rows, []string{wp.WeekStart.Format("2006-01-02"), itoa(wp.Commits), itoa(wp.ActiveAuthors), itoa(wp.MRMerged), itoa(wp.IssuesClosed)})
	}
	rows = append(rows, []string{}, []string{"Важность", "Вывод", "Рекомендация"})
	for _, r := range d.Recommendations {
		rows = append(rows, []string{SeverityText(r.Severity), r.Title, r.Advice})
	}
	if err := cw.WriteAll(rows); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// Num форматирует необязательное число: «—» при отсутствии данных.
func Num(v *float64) string {
	if v == nil {
		return "—"
	}
	if *v == math.Trunc(*v) && math.Abs(*v) < 1e12 {
		return fmt.Sprintf("%.0f", *v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", *v), "0"), ".")
}

// Hours выводит длительность в часах человекочитаемо: «5 ч», «2,5 дн».
func Hours(v *float64) string {
	if v == nil {
		return "—"
	}
	if *v >= 48 {
		d := *v / 24
		return strings.Replace(fmt.Sprintf("%.1f дн", d), ".0 ", " ", 1)
	}
	return fmt.Sprintf("%.1f ч", *v)
}

// Pct выводит процент.
func Pct(v *float64) string {
	if v == nil {
		return "—"
	}
	return Num(v) + "%"
}

// Delta выводит изменение со знаком.
func Delta(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%+.0f%%", *v)
}

// VerdictText — подпись оценки тренда.
func VerdictText(v analysis.Verdict) string {
	switch v {
	case analysis.Improved:
		return "улучшение"
	case analysis.Worsened:
		return "ухудшение"
	case analysis.Stable:
		return "стабильно"
	case analysis.Changed:
		return "изменилось"
	}
	return "нет данных"
}

// SeverityText — подпись важности рекомендации.
func SeverityText(s analysis.Severity) string {
	switch s {
	case analysis.Critical:
		return "Критично"
	case analysis.Warning:
		return "Внимание"
	case analysis.Good:
		return "Хорошо"
	}
	return "Инфо"
}

type bar struct {
	X, Y, W, H float64
	Label      string
	Value      int
}

// bars строит геометрию SVG-гистограммы коммитов по неделям для печатного отчёта.
func bars(weeks []metrics.WeekPoint) []bar {
	maxV := 1
	for _, w := range weeks {
		maxV = max(maxV, w.Commits)
	}
	const width, height = 640.0, 140.0
	n := float64(max(len(weeks), 1))
	step := width / n
	out := make([]bar, 0, len(weeks))
	for i, w := range weeks {
		h := float64(w.Commits) / float64(maxV) * height
		out = append(out, bar{X: float64(i)*step + step*0.15, Y: height - h, W: step * 0.7, H: h,
			Label: w.WeekStart.Format("02.01"), Value: w.Commits})
	}
	return out
}

func itoa(v int) string { return fmt.Sprint(v) }

// Bytes — удобная обёртка для сохранения отчёта в память.
func Bytes(d *Data, format string) ([]byte, error) {
	var buf bytes.Buffer
	err := Render(&buf, d, format)
	return buf.Bytes(), err
}

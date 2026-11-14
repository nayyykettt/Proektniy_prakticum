package web

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"teampulse/internal/analysis"
	"teampulse/internal/domain"
	"teampulse/internal/report"
	"teampulse/internal/zerocracy"
)

var funcs = template.FuncMap{
	"num":      report.Num,
	"hours":    report.Hours,
	"pct":      report.Pct,
	"delta":    report.Delta,
	"value":    formatValue,
	"verdict":  verdictShort,
	"severity": report.SeverityText,
	"sevIcon":  severityIcon,
	"kind":     func(k string) string { return zerocracy.KindTitles[k] },
	"date":     func(t time.Time) string { return t.Format("02.01.2006") },
	"lastDay":  func(t time.Time) string { return t.Add(-time.Second).Format("02.01.2006") },
	"datetime": func(t time.Time) string { return t.Local().Format("02.01.2006 15:04") },
	"ago":      ago,
	"status":   statusText,
	"signed":   func(v int) string { return fmt.Sprintf("%+d", v) },
	"short":    func(s string) string { return s[:min(8, len(s))] },
	"eq64":     func(a, b int64) bool { return a == b },
	"idx2":     func(i int) string { return fmt.Sprintf("%02d", i+1) },
	"sevLabel": severityLabel,
	"plural":   plural,
	"asset":    asset,
	"dir": func(p string) string {
		if i := strings.LastIndexByte(p, '/'); i >= 0 {
			return p[:i+1]
		}
		return ""
	},
	"base":   func(p string) string { return p[strings.LastIndexByte(p, '/')+1:] },
	"unitOf": func(s string) string { _, u := splitUnit(s); return u },
	"numOf":  func(s string) string { v, _ := splitUnit(s); return v },
}

// plural выбирает форму слова по числу: 1 репозиторий, 2 репозитория, 5 репозиториев.
func plural(n int, one, few, many string) string {
	n %= 100
	switch {
	case n >= 11 && n <= 14:
		return many
	case n%10 == 1:
		return one
	case n%10 >= 2 && n%10 <= 4:
		return few
	}
	return many
}

// severityLabel — короткая метка рекомендации в духе «лабораторного журнала».
func severityLabel(s analysis.Severity) string {
	switch s {
	case analysis.Critical:
		return "Риск"
	case analysis.Warning:
		return "Внимание"
	case analysis.Good:
		return "Хорошо"
	}
	return "Инфо"
}

// formatValue выводит значение с учётом единицы измерения.
func formatValue(unit string, v *float64) string {
	switch unit {
	case "ч":
		return report.Hours(v)
	case "%":
		return report.Pct(v)
	case "":
		return report.Num(v)
	}
	if v == nil {
		return "—"
	}
	return report.Num(v) + " " + unit
}

// verdictShort — подпись рядом со стрелкой изменения (цвет никогда не несёт смысл один).
func verdictShort(v analysis.Verdict) string {
	switch v {
	case analysis.Improved:
		return "лучше"
	case analysis.Worsened:
		return "хуже"
	case analysis.Stable:
		return "без изменений"
	case analysis.Changed:
		return "к прошлому периоду"
	}
	return ""
}

func severityIcon(s analysis.Severity) string {
	switch s {
	case analysis.Critical:
		return "⛔"
	case analysis.Warning:
		return "⚠"
	case analysis.Good:
		return "✓"
	}
	return "ℹ"
}

func statusText(s string) string {
	switch s {
	case domain.SyncOK:
		return "синхронизирован"
	case domain.SyncRunning:
		return "синхронизация…"
	case domain.SyncPartial:
		return "частично"
	case domain.SyncFailed:
		return "ошибка"
	}
	return "не синхронизирован"
}

func ago(t *time.Time) string {
	if t == nil {
		return "никогда"
	}
	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	}
	return t.Local().Format("02.01.2006 15:04")
}

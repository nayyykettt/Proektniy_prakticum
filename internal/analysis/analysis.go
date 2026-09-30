// Package analysis интерпретирует метрики: тренды относительно прошлого периода
// и рекомендации на основе прозрачных правил.
package analysis

import (
	"fmt"
	"math"
	"sort"

	"teampulse/internal/metrics"
)

// Verdict — оценка изменения метрики.
type Verdict string

const (
	Improved  Verdict = "improved"
	Worsened  Verdict = "worsened"
	Stable    Verdict = "stable"
	Changed   Verdict = "changed" // нейтральная метрика изменилась
	NoData    Verdict = "no_data"
	stableTol         = 5.0 // изменение менее 5% считаем стабильным
)

// Trend — сравнение метрики с предыдущим периодом той же длины.
type Trend struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Unit     string   `json:"unit"`
	Current  *float64 `json:"current"`
	Previous *float64 `json:"previous"`
	DeltaPct *float64 `json:"delta_pct"`
	Verdict  Verdict  `json:"verdict"`
}

// Trends сравнивает текущий снимок с предыдущим.
func Trends(cur, prev metrics.Summary) []Trend {
	out := make([]Trend, 0, len(metrics.Definitions))
	for _, d := range metrics.Definitions {
		t := Trend{Key: d.Key, Title: d.Title, Unit: d.Unit, Verdict: NoData}
		c, okC := cur.Value(d.Key)
		p, okP := prev.Value(d.Key)
		if okC {
			t.Current = &c
		}
		if okP {
			t.Previous = &p
		}
		if okC && okP {
			t.Verdict = verdict(d.Better, c, p)
			if p != 0 {
				dp := math.Round((c-p)/math.Abs(p)*1000) / 10
				t.DeltaPct = &dp
			}
		}
		out = append(out, t)
	}
	return out
}

func verdict(better metrics.Direction, cur, prev float64) Verdict {
	var change float64
	switch {
	case prev != 0:
		change = (cur - prev) / math.Abs(prev) * 100
	case cur == 0:
		change = 0
	default:
		change = 100
	}
	if math.Abs(change) < stableTol {
		return Stable
	}
	switch better {
	case metrics.HigherBetter:
		if change > 0 {
			return Improved
		}
		return Worsened
	case metrics.LowerBetter:
		if change < 0 {
			return Improved
		}
		return Worsened
	}
	return Changed
}

// Severity — важность рекомендации.
type Severity string

const (
	Critical Severity = "critical"
	Warning  Severity = "warning"
	Info     Severity = "info"
	Good     Severity = "good"
)

// Recommendation — вывод и совет, со ссылкой на метрику-основание.
type Recommendation struct {
	Severity Severity `json:"severity"`
	Metric   string   `json:"metric"`
	Title    string   `json:"title"`
	Advice   string   `json:"advice"`
}

// Recommend строит рекомендации по снимку и трендам.
func Recommend(s metrics.Snapshot, trends []Trend) []Recommendation {
	var out []Recommendation
	add := func(sev Severity, metric, title, advice string) {
		out = append(out, Recommendation{sev, metric, title, advice})
	}
	sm := s.Summary

	if sm.Commits == 0 && sm.MRMerged == 0 {
		add(Info, "commits", "Нет активности за период",
			"Проверьте выбранный период, ветку проекта и дату последней синхронизации.")
		return out
	}
	if sm.BusFactor == 1 && sm.FilesTracked >= 10 && len(s.Ownership) > 0 {
		o := s.Ownership[0]
		add(Critical, "bus_factor", fmt.Sprintf("Bus factor = 1: %s владеет %.0f%% файлов", o.Name, o.Share),
			"Знания сконцентрированы у одного человека. Назначайте ревью и задачи в этих модулях другим участникам, практикуйте парное программирование, документируйте ключевые решения.")
	}
	if v := sm.MRLeadTimeMedianH; v != nil && *v > 72 {
		add(Warning, "mr_lead_time_median_h", fmt.Sprintf("Долгий путь MR до влития: медиана %.0f ч", *v),
			"Дробите задачи на MR поменьше, договоритесь о SLA на ревью (например, 1 рабочий день), ограничьте число одновременно открытых MR (WIP-лимит).")
	}
	if v := sm.FirstReviewMedianH; v != nil && *v > 24 {
		add(Warning, "first_review_median_h", fmt.Sprintf("Ревью начинается в среднем через %.0f ч", *v),
			"Выделите слоты на ревью в течение дня и настройте уведомления/ротацию ревьюеров.")
	}
	if v := sm.LargeMRShare; v != nil && *v > 30 {
		add(Warning, "large_mr_share", fmt.Sprintf("%.0f%% MR больше 400 строк", *v),
			"Большие MR ревьюят поверхностно. Используйте feature flags и декомпозицию на независимые шаги.")
	}
	if v := sm.ReviewCoverage; v != nil && *v < 70 && sm.MRMerged >= 3 {
		add(Warning, "review_coverage", fmt.Sprintf("Только %.0f%% MR прошли ревью", *v),
			"Включите обязательный approve перед влитием в настройках защищённой ветки.")
	}
	if v := sm.TestRatio; v != nil && *v < 20 && sm.Commits >= 10 {
		add(Warning, "test_ratio", fmt.Sprintf("Тесты затрагиваются лишь в %.0f%% коммитов", *v),
			"Добавьте в критерии «готово» наличие тестов на новую логику и проверку покрытия в CI.")
	}
	if v := sm.ReworkRate; v != nil && *v > 25 {
		add(Warning, "rework_rate", fmt.Sprintf("Высокий rework: %.0f%% правок — переделка свежего кода", *v),
			"Уточняйте требования до начала работы, чаще показывайте промежуточный результат, проверьте hotspots ниже.")
	}
	offTotal := deref(sm.OffHoursShare) + deref(sm.WeekendShare)
	if offTotal > 25 && sm.Commits >= 10 {
		names := ""
		for _, a := range s.Authors {
			if a.OffHoursShare != nil && *a.OffHoursShare > 30 && a.Commits >= 5 {
				if names != "" {
					names += ", "
				}
				names += a.Name
			}
		}
		title := fmt.Sprintf("%.0f%% коммитов — вне рабочего времени или в выходные", offTotal)
		advice := "Возможны переработки и риск выгорания. Обсудите нагрузку на ретроспективе."
		if names != "" {
			advice += " Больше всего: " + names + "."
		}
		add(Warning, "off_hours_share", title, advice)
	}
	if v := sm.MessageQuality; v != nil && *v < 60 && sm.Commits >= 10 {
		add(Info, "message_quality", fmt.Sprintf("Информативных сообщений коммитов: %.0f%%", *v),
			"Примите соглашение Conventional Commits (feat:, fix:, refactor:) и добавьте commit-lint в CI.")
	}
	if v := sm.IssueLinkShare; v != nil && *v < 30 && s.HasProviderData && sm.Commits >= 10 {
		add(Info, "issue_link_share", fmt.Sprintf("Только %.0f%% коммитов ссылаются на задачи", *v),
			"Указывайте номер задачи в коммите — так вклад прозрачно учитывается в баллах Zerocracy.")
	}
	if v := sm.LargeCommitShare; v != nil && *v > 20 && sm.Commits >= 10 {
		add(Info, "large_commit_share", fmt.Sprintf("%.0f%% коммитов больше 500 строк", *v),
			"Коммитьте чаще, маленькими логическими шагами — так проще ревью и откат.")
	}
	for _, h := range s.Hotspots {
		if h.Changes >= 10 && h.Authors >= 3 {
			add(Info, "hotspots", fmt.Sprintf("Горячая точка: %s (%d изменений, %d авторов)", h.Path, h.Changes, h.Authors),
				"Частые правки разными людьми — кандидат на рефакторинг или разделение модуля.")
			break
		}
	}
	if !s.HasProviderData {
		add(Info, "mr_merged", "Нет данных о MR и задачах",
			"Подключите проект Т1 Код в настройках — появятся lead time, ревью, задачи и баллы Zerocracy.")
	}

	for _, t := range trends {
		if t.DeltaPct == nil || math.Abs(*t.DeltaPct) < 30 {
			continue
		}
		switch {
		case t.Verdict == Worsened && (t.Key == "mr_lead_time_median_h" || t.Key == "first_review_median_h" || t.Key == "review_coverage"):
			add(Warning, t.Key, fmt.Sprintf("%s ухудшилось на %.0f%% к прошлому периоду", t.Title, math.Abs(*t.DeltaPct)),
				"Разберите на ретроспективе, что изменилось: состав команды, размер задач, релизный цикл.")
		case t.Verdict == Improved && (t.Key == "mr_lead_time_median_h" || t.Key == "test_ratio" || t.Key == "review_coverage" || t.Key == "mr_merged"):
			add(Good, t.Key, fmt.Sprintf("%s улучшилось на %.0f%%", t.Title, math.Abs(*t.DeltaPct)),
				"Зафиксируйте практику, которая дала эффект, в договорённостях команды.")
		}
	}

	order := map[Severity]int{Critical: 0, Warning: 1, Info: 2, Good: 3}
	sort.SliceStable(out, func(i, j int) bool { return order[out[i].Severity] < order[out[j].Severity] })
	return out
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

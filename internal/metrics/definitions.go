package metrics

// Direction — какое изменение метрики считается улучшением.
type Direction string

const (
	HigherBetter Direction = "higher"
	LowerBetter  Direction = "lower"
	Neutral      Direction = "neutral"
)

// Definition — описание метрики для UI (подсказка «как считается») и отчётов.
type Definition struct {
	Key         string    `json:"key"`
	Title       string    `json:"title"`
	Unit        string    `json:"unit"`
	Better      Direction `json:"better"`
	Description string    `json:"description"`
	Group       string    `json:"group"`
}

// Definitions — все ключевые метрики. Порядок = порядок вывода на дашборде.
var Definitions = []Definition{
	{"commits", "Коммиты", "", Neutral, "Число коммитов за период без merge-коммитов. Контекстная метрика — не мера продуктивности сама по себе.", "Поток"},
	{"active_days", "Активные человеко-дни", "", Neutral, "Сумма по разработчикам числа дней, в которые был хотя бы один коммит (по часовому поясу автора).", "Поток"},
	{"commits_per_active_day", "Коммитов на активный день", "", Neutral, "Коммиты / активные человеко-дни. Показывает ритм работы мелкими шагами.", "Поток"},
	{"mr_merged", "Смёрдженные MR", "", HigherBetter, "Число merge request, влитых за период (throughput).", "Поток"},
	{"mr_lead_time_median_h", "Lead time MR (медиана)", "ч", LowerBetter, "Медиана времени от создания MR до влития. Медиана устойчива к единичным «зависшим» MR.", "Поток"},
	{"mr_lead_time_p85_h", "Lead time MR (p85)", "ч", LowerBetter, "85-й перцентиль lead time — «почти худший» типичный случай.", "Поток"},
	{"first_review_median_h", "Время до первого ревью", "ч", LowerBetter, "Медиана времени от создания MR до первого комментария/approve другого участника.", "Поток"},
	{"issues_closed", "Закрытые задачи", "", HigherBetter, "Задачи, закрытые за период.", "Поток"},
	{"issue_cycle_median_h", "Cycle time задачи", "ч", LowerBetter, "Медиана времени от создания до закрытия задачи.", "Поток"},
	{"median_commit_size", "Размер коммита (медиана)", "строк", LowerBetter, "Медиана добавленных+удалённых строк на коммит, без vendor/lock/сгенерированных файлов. Маленькие коммиты проще ревьюить и откатывать.", "Качество"},
	{"large_commit_share", "Доля больших коммитов", "%", LowerBetter, "Доля коммитов больше 500 строк.", "Качество"},
	{"median_mr_size", "Размер MR (медиана)", "строк", LowerBetter, "Медиана добавленных+удалённых строк в влитых MR.", "Качество"},
	{"large_mr_share", "Доля больших MR", "%", LowerBetter, "Доля влитых MR больше 400 строк — после этого порога качество ревью резко падает.", "Качество"},
	{"rework_rate", "Rework rate", "%", LowerBetter, "Доля изменённых строк в файлах, которые тот же автор уже менял за последние 21 день (доработка «свежего» кода).", "Качество"},
	{"test_ratio", "Коммиты с тестами", "%", HigherBetter, "Доля коммитов с кодом, затрагивающих тестовые файлы (*_test.go, tests/, *.spec.ts и т.п.).", "Качество"},
	{"message_quality", "Качество сообщений", "%", HigherBetter, "Доля коммитов с информативным сообщением: Conventional Commits или тема 10–100 символов, не «fix»/«wip».", "Качество"},
	{"issue_link_share", "Связь с задачами", "%", HigherBetter, "Доля коммитов со ссылкой на задачу (#12, PROJ-34) — прослеживаемость работы.", "Качество"},
	{"review_coverage", "Покрытие ревью", "%", HigherBetter, "Доля влитых MR, получивших хотя бы одно ревью от другого участника.", "Качество"},
	{"bus_factor", "Bus factor", "", HigherBetter, "Минимальное число людей, владеющих более чем 50% активных файлов (владелец файла — автор наибольшего числа изменённых строк). 1 — критический риск.", "Команда"},
	{"reviews_given", "Проведено ревью", "", HigherBetter, "Число MR, в ревью которых участвовали разработчики.", "Команда"},
	{"off_hours_share", "Работа вне рабочего времени", "%", LowerBetter, "Доля коммитов в будни вне рабочего интервала (по местному времени автора). Сигнал переработок.", "Команда"},
	{"weekend_share", "Работа в выходные", "%", LowerBetter, "Доля коммитов в субботу и воскресенье.", "Команда"},
}

// DefinitionByKey возвращает описание метрики.
func DefinitionByKey(key string) (Definition, bool) {
	for _, d := range Definitions {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}

// Value достаёт значение метрики из Summary по ключу. ok=false — нет данных.
func (s Summary) Value(key string) (float64, bool) {
	ints := map[string]int{
		"commits": s.Commits, "active_days": s.ActiveDays, "mr_merged": s.MRMerged,
		"issues_closed": s.IssuesClosed, "bus_factor": s.BusFactor, "reviews_given": s.ReviewsGiven,
	}
	if v, ok := ints[key]; ok {
		return float64(v), true
	}
	ptrs := map[string]*float64{
		"commits_per_active_day": s.CommitsPerActiveDay, "mr_lead_time_median_h": s.MRLeadTimeMedianH,
		"mr_lead_time_p85_h": s.MRLeadTimeP85H, "first_review_median_h": s.FirstReviewMedianH,
		"issue_cycle_median_h": s.IssueCycleMedianH, "median_commit_size": s.MedianCommitSize,
		"large_commit_share": s.LargeCommitShare, "median_mr_size": s.MedianMRSize, "large_mr_share": s.LargeMRShare,
		"rework_rate": s.ReworkRate, "test_ratio": s.TestRatio, "message_quality": s.MessageQuality,
		"issue_link_share": s.IssueLinkShare, "review_coverage": s.ReviewCoverage,
		"off_hours_share": s.OffHoursShare, "weekend_share": s.WeekendShare,
	}
	if p := ptrs[key]; p != nil {
		return *p, true
	}
	return 0, false
}

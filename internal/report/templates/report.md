# Отчёт о производительности: {{.Project.Name}}

{{.Scope}} · период {{date .Period.From}} — {{lastDay .Period.To}} · сформирован {{datetime .GeneratedAt}}

## Ключевые метрики

| Метрика | Значение | Прошлый период | Изменение | Оценка |
|---|---:|---:|---:|---|
{{range .Trends}}{{if .Current}}| {{.Title}} | {{num .Current}} {{.Unit}} | {{num .Previous}} | {{delta .DeltaPct}} | {{verdict .Verdict}} |
{{end}}{{end}}
{{with .Recommendations}}
## Выводы и рекомендации
{{range .}}
- **{{severity .Severity}}: {{.Title}}.** {{.Advice}}{{end}}
{{end}}
## Вклад разработчиков

| Разработчик | Коммиты | Акт. дни | Медиана коммита | С тестами | MR | Lead time | Ревью | Задачи |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
{{range .Snapshot.Authors}}| {{.Name}} | {{.Commits}} | {{.ActiveDays}} | {{num .MedianCommitSize}} | {{pct .TestRatio}} | {{.MRMerged}} | {{hours .MRLeadTimeMedianH}} | {{.ReviewsGiven}} | {{.IssuesClosed}} |
{{end}}
{{with .Contribution.Scores}}
## Баллы Zerocracy

| Разработчик | Баллы | Доля |
|---|---:|---:|
{{range .}}| {{.Name}} | {{.Points}} | {{.Share}}% |
{{end}}{{end}}
{{with .Snapshot.Hotspots}}
## Горячие точки кода

| Файл | Изменений | Строк | Авторов |
|---|---:|---:|---:|
{{range .}}| `{{.Path}}` | {{.Changes}} | {{.Churn}} | {{.Authors}} |
{{end}}{{end}}
**Bus factor:** {{.Snapshot.Summary.BusFactor}} (активных файлов: {{.Snapshot.Summary.FilesTracked}})
{{with .Suggestions}}
## Распределение открытых задач
{{range .}}{{$c := index .Candidates 0}}
- #{{.IssueIID}} {{.Title}} → **{{$c.Name}}** ({{$c.Reason}}){{end}}
{{end}}
---
_Методика: merge-коммиты и vendor/lock/сгенерированные файлы исключены, для длительностей — медианы, строки кода — только контекст. Формулы — docs/metrics.md._

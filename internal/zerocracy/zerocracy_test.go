package zerocracy

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"teampulse/internal/domain"
	"teampulse/internal/metrics"
)

var t0 = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

func day(d int) time.Time { return t0.AddDate(0, 0, d) }
func pd(d int) *time.Time { t := day(d); return &t }

func dataset() *domain.Dataset {
	return &domain.Dataset{
		Authors: map[int64]domain.Author{1: {ID: 1, Name: "Иван", Login: "ivan"}, 2: {ID: 2, Name: "Анна", Login: "anna"}},
		Commits: []domain.Commit{
			{SHA: "aaaaaaaaaa", AuthorID: 1, AuthoredAt: day(1), Message: "feat: parser #1",
				Files: []domain.FileChange{{Path: "internal/gitparse/parser.go", Additions: 100}}},
			{SHA: "bbbbbbbbbb", AuthorID: 2, AuthoredAt: day(1), Message: "feat: dashboard ui",
				Files: []domain.FileChange{{Path: "web/templates/dashboard.html", Additions: 80}}},
		},
		MergeRequests: []domain.MergeRequest{
			{IID: 1, Title: "parser", AuthorID: 1, CreatedAt: day(0), MergedAt: pd(2), Additions: 100, Deletions: 0,
				Reviews: []domain.Review{{AuthorID: 2, At: day(1), Comments: 9}}},
			{IID: 2, Title: "big", AuthorID: 2, CreatedAt: day(0), MergedAt: pd(3), Additions: 900, Deletions: 0},
		},
		Issues: []domain.Issue{
			{IID: 1, Title: "Парсер", AssigneeID: 1, Labels: []string{"backend", "size::M"}, CreatedAt: day(-1), ClosedAt: pd(2)},
			{IID: 2, Title: "Старая задача", AssigneeID: 2, Labels: []string{"frontend"}, CreatedAt: day(-30)},
			{IID: 3, Title: "Ускорить parser истории", Labels: []string{"backend"}, CreatedAt: day(4)},
			{IID: 4, Title: "Новый dashboard графиков", Labels: []string{"frontend"}, CreatedAt: day(4)},
		},
	}
}

func TestEvaluate(t *testing.T) {
	p := domain.Period{From: t0, To: day(7)}
	c := Evaluate(dataset(), p, nil, DefaultPolicy())
	scores := map[string]Score{}
	for _, s := range c.Scores {
		scores[s.Name] = s
	}
	// Иван: MR 10 + компактный 5 + задача size::M 10 + коммит со ссылкой 1 = 26.
	if got := scores["Иван"].Points; got != 26 {
		t.Errorf("Иван: %d баллов, breakdown %v", got, scores["Иван"].Breakdown)
	}
	// Анна: MR 10 − огромный 5 − без ревью 3 + ревью (3 + 5 комм. в зачёт) − просрочка 2 = 8.
	if got := scores["Анна"].Points; got != 8 {
		t.Errorf("Анна: %d баллов, breakdown %v", got, scores["Анна"].Breakdown)
	}
	if c.Scores[0].Name != "Иван" || c.Total != 34 {
		t.Errorf("порядок/итог: %+v total=%d", c.Scores, c.Total)
	}
	// Воспроизводимость.
	again := Evaluate(dataset(), p, nil, DefaultPolicy())
	if again.Total != c.Total || len(again.Events) != len(c.Events) {
		t.Error("оценка должна быть детерминированной")
	}
	// Фильтр команды.
	only := Evaluate(dataset(), p, metrics.Filter{1: true}, DefaultPolicy())
	if len(only.Scores) != 1 || only.Scores[0].Name != "Иван" {
		t.Errorf("фильтр: %+v", only.Scores)
	}
}

func TestSuggest(t *testing.T) {
	s := Suggest(dataset(), day(5), nil, 0)
	if len(s) != 2 {
		t.Fatalf("ожидалось 2 неназначенные задачи, got %d", len(s))
	}
	byIssue := map[int]Suggestion{}
	for _, x := range s {
		byIssue[x.IssueIID] = x
	}
	if top := byIssue[3].Candidates[0]; top.Name != "Иван" || top.Expertise != 1 {
		t.Errorf("задача про parser/backend → Иван, got %+v", byIssue[3].Candidates)
	}
	if top := byIssue[4].Candidates[0]; top.Name != "Анна" {
		t.Errorf("задача про dashboard → Анна, got %+v", byIssue[4].Candidates)
	}
}

func TestLoadPolicy(t *testing.T) {
	p, err := LoadPolicy("")
	if err != nil || p.MRMerged != 10 {
		t.Fatal("политика по умолчанию")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "p.json")
	_ = os.WriteFile(path, []byte(`{"mr_merged": 20}`), 0o600)
	p, err = LoadPolicy(path)
	if err != nil || p.MRMerged != 20 || p.Review != 3 {
		t.Fatalf("частичное переопределение: %+v %v", p, err)
	}
	_ = os.WriteFile(path, []byte(`{bad`), 0o600)
	if _, err := LoadPolicy(path); err == nil {
		t.Fatal("битый JSON должен давать ошибку")
	}
}

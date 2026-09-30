package metrics

import (
	"testing"
	"time"

	"teampulse/internal/domain"
)

var t0 = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC) // понедельник

func at(day, hour int) time.Time { return t0.Add(time.Duration(day*24+hour) * time.Hour) }

func commit(sha string, author int64, when time.Time, msg string, files ...domain.FileChange) domain.Commit {
	c := domain.Commit{SHA: sha, AuthorID: author, AuthoredAt: when, Message: msg, Files: files}
	for _, f := range files {
		if !f.Excluded {
			c.Additions += f.Additions
			c.Deletions += f.Deletions
			c.FilesChanged++
		}
	}
	return c
}

func file(path string, add, del int) domain.FileChange {
	return domain.FileChange{Path: path, Additions: add, Deletions: del}
}

func dataset() *domain.Dataset {
	return &domain.Dataset{
		Authors: map[int64]domain.Author{1: {ID: 1, Name: "Иван"}, 2: {ID: 2, Name: "Анна"}},
		Commits: []domain.Commit{
			commit("c0", 1, at(-30, 10), "feat: old", file("a.go", 100, 0)), // до периода
			commit("c1", 1, at(0, 10), "feat: add parser #12", file("a.go", 10, 2), domain.FileChange{Path: "a_test.go", Additions: 5, IsTest: true}),
			commit("c2", 1, at(1, 22), "fix", file("a.go", 3, 1)),                        // вне рабочего времени, rework
			commit("c3", 2, at(5, 12), "docs: readme update", file("README.md", 600, 0)), // суббота, большой
			commit("c4", 2, at(2, 11), "refactor(api): split handlers", file("b.go", 20, 5)),
			{SHA: "m1", AuthorID: 1, AuthoredAt: at(2, 12), IsMerge: true, Message: "Merge branch"},
			commit("c5", 2, at(3, 11), "chore: bump deps", domain.FileChange{Path: "go.sum", Additions: 999, Excluded: true}),
		},
		MergeRequests: []domain.MergeRequest{
			{IID: 1, AuthorID: 1, CreatedAt: at(0, 9), MergedAt: ptrT(at(1, 9)), Additions: 12, Deletions: 2,
				Reviews: []domain.Review{{AuthorID: 2, At: at(0, 11)}}},
			{IID: 2, AuthorID: 2, CreatedAt: at(2, 9), MergedAt: ptrT(at(4, 9)), Additions: 500, Deletions: 0},
			{IID: 3, AuthorID: 2, CreatedAt: at(3, 9), Additions: -1, Deletions: -1},
		},
		Issues: []domain.Issue{
			{IID: 1, AssigneeID: 1, CreatedAt: at(-2, 0), ClosedAt: ptrT(at(1, 0))},
			{IID: 2, AssigneeID: 2, CreatedAt: at(1, 0)},
		},
	}
}

func ptrT(t time.Time) *time.Time { return &t }

func TestComputeSummary(t *testing.T) {
	p := domain.Period{From: t0, To: t0.AddDate(0, 0, 7)}
	s := Compute(dataset(), p, nil, DefaultOptions())
	sm := s.Summary

	if sm.Commits != 5 || sm.MergeCommits != 1 {
		t.Fatalf("коммиты: %d, merge: %d", sm.Commits, sm.MergeCommits)
	}
	if sm.ActiveAuthors != 2 || sm.ActiveDays != 5 {
		t.Fatalf("авторы %d, человеко-дни %d", sm.ActiveAuthors, sm.ActiveDays)
	}
	// Размеры: 17, 4, 600, 25, 0 → медиана 17.
	eq(t, "median_commit_size", sm.MedianCommitSize, 17)
	eq(t, "large_commit_share", sm.LargeCommitShare, 20)
	// Тесты: из 4 коммитов с кодом (c5 только go.sum) тесты у одного.
	eq(t, "test_ratio", sm.TestRatio, 25)
	// Сообщения: хорошие c1, c3, c4, c5; «fix» — плохое.
	eq(t, "message_quality", sm.MessageQuality, 80)
	eq(t, "issue_link_share", sm.IssueLinkShare, 20)
	eq(t, "off_hours_share", sm.OffHoursShare, 20)
	eq(t, "weekend_share", sm.WeekendShare, 20)
	// Rework: c1 трогает a.go после c0 (>21 дня — не rework), c2 трогает a.go через 36ч → 4 строки.
	// Всего строк 17+4+600+25 = 646.
	eq(t, "rework_rate", sm.ReworkRate, 0.62)

	if sm.MRMerged != 2 || sm.MROpened != 3 {
		t.Fatalf("MR merged %d opened %d", sm.MRMerged, sm.MROpened)
	}
	eq(t, "lead median", sm.MRLeadTimeMedianH, 36) // 24ч и 48ч
	eq(t, "first review", sm.FirstReviewMedianH, 2)
	eq(t, "large_mr_share", sm.LargeMRShare, 50)
	eq(t, "review_coverage", sm.ReviewCoverage, 50)
	if sm.ReviewsGiven != 1 || sm.IssuesClosed != 1 || sm.IssuesOpened != 1 {
		t.Fatalf("reviews %d closed %d opened %d", sm.ReviewsGiven, sm.IssuesClosed, sm.IssuesOpened)
	}
	eq(t, "issue cycle", sm.IssueCycleMedianH, 72)

	if s.Heatmap[5][12] != 1 || s.Heatmap[1][22] != 1 {
		t.Fatal("тепловая карта: неверные ячейки")
	}
	if len(s.Weekly) != 1 || s.Weekly[0].Commits != 5 || s.Weekly[0].MRMerged != 2 {
		t.Fatalf("недельный ряд: %+v", s.Weekly)
	}
	if s.Hotspots[0].Path != "a.go" || s.Hotspots[0].Changes != 2 {
		t.Fatalf("hotspots: %+v", s.Hotspots)
	}
}

func TestBusFactorAndOwnership(t *testing.T) {
	p := domain.Period{From: t0, To: t0.AddDate(0, 0, 7)}
	s := Compute(dataset(), p, nil, DefaultOptions())
	// Файлы: a.go, a_test.go (Иван), README.md, b.go (Анна) → 2 и 2, bus factor = 2? Нет:
	// покрыть > 50% (больше 2 из 4) может только пара → 2.
	if s.Summary.FilesTracked != 4 || s.Summary.BusFactor != 2 {
		t.Fatalf("files %d bus factor %d", s.Summary.FilesTracked, s.Summary.BusFactor)
	}
	ds := dataset()
	ds.Commits = append(ds.Commits, commit("c6", 1, at(4, 10), "feat: more files", file("c.go", 1, 0), file("d.go", 1, 0)))
	s = Compute(ds, p, nil, DefaultOptions())
	if s.Summary.BusFactor != 1 {
		t.Fatalf("у Ивана 4 из 6 файлов → bus factor 1, got %d", s.Summary.BusFactor)
	}
}

func TestTeamFilterAndAuthors(t *testing.T) {
	p := domain.Period{From: t0, To: t0.AddDate(0, 0, 7)}
	s := Compute(dataset(), p, Filter{2: true}, DefaultOptions())
	if s.Summary.Commits != 3 || s.Summary.MRMerged != 1 || s.Summary.IssuesClosed != 0 {
		t.Fatalf("фильтр команды: %+v", s.Summary)
	}
	if len(s.Authors) != 1 || s.Authors[0].Name != "Анна" {
		t.Fatalf("authors: %+v", s.Authors)
	}
}

func TestEmptyData(t *testing.T) {
	p := domain.Period{From: t0, To: t0.AddDate(0, 1, 0)}
	s := Compute(&domain.Dataset{Authors: map[int64]domain.Author{}}, p, nil, DefaultOptions())
	if s.Summary.Commits != 0 || s.Summary.MedianCommitSize != nil || s.Summary.TestRatio != nil || s.Summary.BusFactor != 0 {
		t.Fatalf("пустые данные должны давать nil-метрики: %+v", s.Summary)
	}
	if len(s.Weekly) != 5 {
		t.Fatalf("март 2026 с 2-го числа — 5 недель, got %d", len(s.Weekly))
	}
}

func TestGoodMessage(t *testing.T) {
	good := []string{"feat(api): add endpoint", "fix: null pointer", "Добавлен парсер Git-истории", "Refactor metrics calculation"}
	bad := []string{"fix", "wip", "update", "правки", "asdf", "."}
	for _, m := range good {
		if !GoodMessage(m) {
			t.Errorf("%q должно быть хорошим", m)
		}
	}
	for _, m := range bad {
		if GoodMessage(m) {
			t.Errorf("%q должно быть плохим", m)
		}
	}
	if !HasIssueRef("closes #12") || !HasIssueRef("PROJ-7 fix") || HasIssueRef("no refs") {
		t.Error("HasIssueRef")
	}
}

func TestPercentile(t *testing.T) {
	eq(t, "median odd", median([]float64{3, 1, 2}), 2)
	eq(t, "median even", median([]float64{1, 2, 3, 4}), 2.5)
	eq(t, "p85", percentile([]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, 0.85), 9.5)
	if median(nil) != nil {
		t.Error("медиана пустого набора должна быть nil")
	}
}

func eq(t *testing.T, name string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: nil, want %v", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s: got %v, want %v", name, *got, want)
	}
}

func TestWithoutBots(t *testing.T) {
	ds := dataset()
	ds.Authors[3] = domain.Author{ID: 3, Name: "dependabot[bot]", Email: "49699333+dependabot[bot]@users.noreply.github.com"}
	ds.Commits = append(ds.Commits, commit("b1", 3, at(1, 3), "build(deps): bump x", file("go.mod", 1, 1)))
	ds.MergeRequests[0].Reviews = append(ds.MergeRequests[0].Reviews, domain.Review{AuthorID: 3, At: at(0, 12)})
	clean := WithoutBots(ds)
	if len(clean.Commits) != len(ds.Commits)-1 || len(clean.MergeRequests[0].Reviews) != 1 {
		t.Fatalf("бот не исключён: %d коммитов, %d ревью", len(clean.Commits), len(clean.MergeRequests[0].Reviews))
	}
	if len(ds.MergeRequests[0].Reviews) != 2 {
		t.Fatal("исходный набор не должен изменяться")
	}
	if IsBot(domain.Author{Name: "Иван", Email: "ivan@corp.ru"}) {
		t.Fatal("человек распознан как бот")
	}
}

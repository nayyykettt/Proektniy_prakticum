// Package metrics рассчитывает метрики производительности команды за период.
// Все функции чистые: на вход — данные, на выход — снимок метрик.
package metrics

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"teampulse/internal/domain"
)

// Options — пороги и настройки расчёта.
type Options struct {
	WorkdayStart     int           // начало рабочего дня, час
	WorkdayEnd       int           // конец рабочего дня, час
	LargeCommitLines int           // «большой» коммит, строк
	LargeMRLines     int           // «большой» MR, строк
	ReworkWindow     time.Duration // окно доработки недавнего кода
	OwnershipWindow  time.Duration // какие файлы считаются «живыми» для bus factor
	HotspotsLimit    int
}

// DefaultOptions — значения по умолчанию (обоснование в docs/metrics.md).
func DefaultOptions() Options {
	return Options{
		WorkdayStart:     9,
		WorkdayEnd:       19,
		LargeCommitLines: 500,
		LargeMRLines:     400,
		ReworkWindow:     21 * 24 * time.Hour,
		OwnershipWindow:  365 * 24 * time.Hour,
		HotspotsLimit:    10,
	}
}

// Summary — агрегированные метрики. nil означает «нет данных для расчёта».
type Summary struct {
	Commits             int      `json:"commits"`
	MergeCommits        int      `json:"merge_commits"`
	ActiveAuthors       int      `json:"active_authors"`
	ActiveDays          int      `json:"active_days"` // человеко-дни с коммитами
	CommitsPerActiveDay *float64 `json:"commits_per_active_day"`
	Additions           int      `json:"additions"`
	Deletions           int      `json:"deletions"`
	MedianCommitSize    *float64 `json:"median_commit_size"`
	LargeCommitShare    *float64 `json:"large_commit_share"`
	ReworkRate          *float64 `json:"rework_rate"`
	TestRatio           *float64 `json:"test_ratio"`
	MessageQuality      *float64 `json:"message_quality"`
	IssueLinkShare      *float64 `json:"issue_link_share"`
	OffHoursShare       *float64 `json:"off_hours_share"`
	WeekendShare        *float64 `json:"weekend_share"`
	BusFactor           int      `json:"bus_factor"`
	FilesTracked        int      `json:"files_tracked"`

	MROpened           int      `json:"mr_opened"`
	MRMerged           int      `json:"mr_merged"`
	MRLeadTimeMedianH  *float64 `json:"mr_lead_time_median_h"`
	MRLeadTimeP85H     *float64 `json:"mr_lead_time_p85_h"`
	FirstReviewMedianH *float64 `json:"first_review_median_h"`
	MedianMRSize       *float64 `json:"median_mr_size"`
	LargeMRShare       *float64 `json:"large_mr_share"`
	ReviewCoverage     *float64 `json:"review_coverage"`
	ReviewsGiven       int      `json:"reviews_given"`
	IssuesOpened       int      `json:"issues_opened"`
	IssuesClosed       int      `json:"issues_closed"`
	IssueCycleMedianH  *float64 `json:"issue_cycle_median_h"`
}

// AuthorStats — вклад одного разработчика.
type AuthorStats struct {
	AuthorID          int64    `json:"author_id"`
	Name              string   `json:"name"`
	Email             string   `json:"email,omitempty"`
	Commits           int      `json:"commits"`
	ActiveDays        int      `json:"active_days"`
	Additions         int      `json:"additions"`
	Deletions         int      `json:"deletions"`
	MedianCommitSize  *float64 `json:"median_commit_size"`
	TestRatio         *float64 `json:"test_ratio"`
	MessageQuality    *float64 `json:"message_quality"`
	ReworkRate        *float64 `json:"rework_rate"`
	OffHoursShare     *float64 `json:"off_hours_share"`
	MRMerged          int      `json:"mr_merged"`
	MRLeadTimeMedianH *float64 `json:"mr_lead_time_median_h"`
	ReviewsGiven      int      `json:"reviews_given"`
	IssuesClosed      int      `json:"issues_closed"`
	FilesOwned        int      `json:"files_owned"`
}

// WeekPoint — значения за неделю (понедельник, UTC).
type WeekPoint struct {
	WeekStart     time.Time `json:"week_start"`
	Commits       int       `json:"commits"`
	Additions     int       `json:"additions"`
	Deletions     int       `json:"deletions"`
	ActiveAuthors int       `json:"active_authors"`
	MRMerged      int       `json:"mr_merged"`
	MRLeadMedianH *float64  `json:"mr_lead_median_h"`
	IssuesClosed  int       `json:"issues_closed"`
}

// Hotspot — часто изменяемый файл (кандидат на рефакторинг).
type Hotspot struct {
	Path    string `json:"path"`
	Changes int    `json:"changes"`
	Churn   int    `json:"churn"`
	Authors int    `json:"authors"`
}

// OwnerShare — доля файлов, которыми «владеет» автор.
type OwnerShare struct {
	AuthorID int64   `json:"author_id"`
	Name     string  `json:"name"`
	Files    int     `json:"files"`
	Share    float64 `json:"share"`
}

// Snapshot — полный набор метрик за период.
type Snapshot struct {
	Period          domain.Period `json:"period"`
	Summary         Summary       `json:"summary"`
	Authors         []AuthorStats `json:"authors"`
	Weekly          []WeekPoint   `json:"weekly"`
	Heatmap         [7][24]int    `json:"heatmap"` // [понедельник..воскресенье][час]
	Hotspots        []Hotspot     `json:"hotspots"`
	Ownership       []OwnerShare  `json:"ownership"`
	HasProviderData bool          `json:"has_provider_data"`
}

// Filter ограничивает расчёт набором авторов (команда). nil — все авторы.
type Filter map[int64]bool

func (f Filter) has(id int64) bool { return f == nil || f[id] }

// Compute рассчитывает снимок метрик за период.
func Compute(ds *domain.Dataset, p domain.Period, f Filter, opt Options) Snapshot {
	s := Snapshot{Period: p, HasProviderData: len(ds.MergeRequests)+len(ds.Issues) > 0}
	acc := map[int64]*authorAcc{}
	get := func(id int64) *authorAcc {
		if a, ok := acc[id]; ok {
			return a
		}
		a := &authorAcc{days: map[string]bool{}}
		acc[id] = a
		return a
	}

	computeCommits(ds, p, f, opt, &s, get)
	computeProvider(ds, p, f, opt, &s, get)
	owned := computeOwnership(ds, p, f, opt, &s)
	s.Weekly = weekly(ds, p, f)

	for id, a := range acc {
		au := ds.Authors[id]
		st := AuthorStats{
			AuthorID: id, Name: au.Name, Email: au.Email,
			Commits: a.commits, ActiveDays: len(a.days), Additions: a.add, Deletions: a.del,
			MedianCommitSize:  median(a.sizes),
			TestRatio:         ratio(a.withTests, a.codeCommits),
			MessageQuality:    ratio(a.goodMsg, a.commits),
			ReworkRate:        ratio(a.rework, a.lines),
			OffHoursShare:     ratio(a.offHours+a.weekend, a.commits),
			MRMerged:          len(a.leadTimes),
			MRLeadTimeMedianH: median(a.leadTimes),
			ReviewsGiven:      a.reviews,
			IssuesClosed:      a.issuesClosed,
			FilesOwned:        owned[id],
		}
		if st.Name == "" {
			st.Name = "unknown"
		}
		s.Authors = append(s.Authors, st)
	}
	sort.Slice(s.Authors, func(i, j int) bool {
		ai, aj := s.Authors[i], s.Authors[j]
		if ai.Commits+ai.MRMerged != aj.Commits+aj.MRMerged {
			return ai.Commits+ai.MRMerged > aj.Commits+aj.MRMerged
		}
		return ai.Name < aj.Name
	})
	return s
}

type authorAcc struct {
	commits, codeCommits, withTests, goodMsg   int
	add, del, lines, rework, offHours, weekend int
	days                                       map[string]bool
	sizes, leadTimes                           []float64
	reviews, issuesClosed                      int
}

func computeCommits(ds *domain.Dataset, p domain.Period, f Filter, opt Options, s *Snapshot, get func(int64) *authorAcc) {
	var sizes []float64
	var large, codeCommits, withTests, goodMsg, linked, offHours, weekend, lines, rework int
	personDays := map[string]bool{}
	authors := map[int64]bool{}
	lastTouch := map[string]time.Time{} // "author|path" → время последнего изменения
	type hs struct {
		changes, churn int
		authors        map[int64]bool
	}
	hot := map[string]*hs{}

	for _, c := range ds.Commits { // по возрастанию времени
		inScope := p.Contains(c.AuthoredAt) && f.has(c.AuthorID)
		if inScope && c.IsMerge {
			s.Summary.MergeCommits++
		}
		if c.IsMerge {
			continue
		}
		var a *authorAcc
		if inScope {
			a = get(c.AuthorID)
			s.Summary.Commits++
			a.commits++
			authors[c.AuthorID] = true
			local := c.LocalTime()
			day := local.Format("2006-01-02")
			personDays[fmtID(c.AuthorID)+day] = true
			a.days[day] = true

			size := float64(c.Additions + c.Deletions)
			sizes = append(sizes, size)
			a.sizes = append(a.sizes, size)
			if int(size) > opt.LargeCommitLines {
				large++
			}
			s.Summary.Additions += c.Additions
			s.Summary.Deletions += c.Deletions
			a.add += c.Additions
			a.del += c.Deletions

			if GoodMessage(c.Message) {
				goodMsg++
				a.goodMsg++
			}
			if HasIssueRef(c.Message) {
				linked++
			}
			wd := (int(local.Weekday()) + 6) % 7 // понедельник = 0
			s.Heatmap[wd][local.Hour()]++
			switch {
			case wd >= 5:
				weekend++
				a.weekend++
			case local.Hour() < opt.WorkdayStart || local.Hour() >= opt.WorkdayEnd:
				offHours++
				a.offHours++
			}
		}

		touchesCode, touchesTests := false, false
		for _, fc := range c.Files {
			if fc.Excluded {
				continue
			}
			key := fmtID(c.AuthorID) + "|" + fc.Path
			if inScope {
				touchesCode = true
				touchesTests = touchesTests || fc.IsTest
				n := fc.Additions + fc.Deletions
				lines += n
				a.lines += n
				if last, ok := lastTouch[key]; ok && c.AuthoredAt.Sub(last) <= opt.ReworkWindow {
					rework += n
					a.rework += n
				}
				h := hot[fc.Path]
				if h == nil {
					h = &hs{authors: map[int64]bool{}}
					hot[fc.Path] = h
				}
				h.changes++
				h.churn += n
				h.authors[c.AuthorID] = true
			}
			lastTouch[key] = c.AuthoredAt
		}
		if inScope && touchesCode {
			codeCommits++
			a.codeCommits++
			if touchesTests {
				withTests++
				a.withTests++
			}
		}
	}

	sm := &s.Summary
	sm.ActiveAuthors = len(authors)
	sm.ActiveDays = len(personDays)
	sm.CommitsPerActiveDay = ratioPlain(sm.Commits, sm.ActiveDays)
	sm.MedianCommitSize = median(sizes)
	sm.LargeCommitShare = ratio(large, sm.Commits)
	sm.ReworkRate = ratio(rework, lines)
	sm.TestRatio = ratio(withTests, codeCommits)
	sm.MessageQuality = ratio(goodMsg, sm.Commits)
	sm.IssueLinkShare = ratio(linked, sm.Commits)
	sm.OffHoursShare = ratio(offHours, sm.Commits)
	sm.WeekendShare = ratio(weekend, sm.Commits)

	for path, h := range hot {
		s.Hotspots = append(s.Hotspots, Hotspot{Path: path, Changes: h.changes, Churn: h.churn, Authors: len(h.authors)})
	}
	sort.Slice(s.Hotspots, func(i, j int) bool {
		a, b := s.Hotspots[i], s.Hotspots[j]
		if a.Changes != b.Changes {
			return a.Changes > b.Changes
		}
		if a.Churn != b.Churn {
			return a.Churn > b.Churn
		}
		return a.Path < b.Path
	})
	if len(s.Hotspots) > opt.HotspotsLimit {
		s.Hotspots = s.Hotspots[:opt.HotspotsLimit]
	}
}

func computeProvider(ds *domain.Dataset, p domain.Period, f Filter, opt Options, s *Snapshot, get func(int64) *authorAcc) {
	var lead, firstReview, mrSizes []float64
	var large, reviewed int
	sm := &s.Summary
	for _, mr := range ds.MergeRequests {
		if p.Contains(mr.CreatedAt) && f.has(mr.AuthorID) {
			sm.MROpened++
			if fr := firstReviewAt(mr); fr != nil {
				firstReview = append(firstReview, hours(fr.Sub(mr.CreatedAt)))
			}
		}
		for _, r := range mr.Reviews {
			if p.Contains(r.At) && f.has(r.AuthorID) {
				sm.ReviewsGiven++
				get(r.AuthorID).reviews++
			}
		}
		if mr.MergedAt == nil || !p.Contains(*mr.MergedAt) || !f.has(mr.AuthorID) {
			continue
		}
		sm.MRMerged++
		lt := hours(mr.MergedAt.Sub(mr.CreatedAt))
		lead = append(lead, lt)
		a := get(mr.AuthorID)
		a.leadTimes = append(a.leadTimes, lt)
		if len(mr.Reviews) > 0 {
			reviewed++
		}
		if mr.Additions >= 0 {
			size := mr.Additions + mr.Deletions
			mrSizes = append(mrSizes, float64(size))
			if size > opt.LargeMRLines {
				large++
			}
		}
	}
	sm.MRLeadTimeMedianH = median(lead)
	sm.MRLeadTimeP85H = percentile(lead, 0.85)
	sm.FirstReviewMedianH = median(firstReview)
	sm.MedianMRSize = median(mrSizes)
	sm.LargeMRShare = ratio(large, len(mrSizes))
	sm.ReviewCoverage = ratio(reviewed, sm.MRMerged)

	var cycle []float64
	for _, is := range ds.Issues {
		if p.Contains(is.CreatedAt) && (f == nil || f.has(is.AssigneeID)) {
			sm.IssuesOpened++
		}
		if is.ClosedAt == nil || !p.Contains(*is.ClosedAt) {
			continue
		}
		if f != nil && !f.has(is.AssigneeID) {
			continue
		}
		sm.IssuesClosed++
		cycle = append(cycle, hours(is.ClosedAt.Sub(is.CreatedAt)))
		if is.AssigneeID != 0 {
			get(is.AssigneeID).issuesClosed++
		}
	}
	sm.IssueCycleMedianH = median(cycle)
}

func firstReviewAt(mr domain.MergeRequest) *time.Time {
	if mr.FirstReviewAt != nil {
		return mr.FirstReviewAt
	}
	if len(mr.Reviews) > 0 {
		t := mr.Reviews[0].At
		return &t
	}
	return nil
}

// computeOwnership определяет владельца каждого «живого» файла (автор с наибольшим
// числом изменённых строк) и bus factor — минимальное число людей, владеющих > 50% файлов.
func computeOwnership(ds *domain.Dataset, p domain.Period, f Filter, opt Options, s *Snapshot) map[int64]int {
	since := p.To.Add(-opt.OwnershipWindow)
	alive := map[string]bool{}
	lines := map[string]map[int64]int{}
	for _, c := range ds.Commits {
		if c.IsMerge || !c.AuthoredAt.Before(p.To) || !f.has(c.AuthorID) {
			continue
		}
		for _, fc := range c.Files {
			if fc.Excluded {
				continue
			}
			if lines[fc.Path] == nil {
				lines[fc.Path] = map[int64]int{}
			}
			lines[fc.Path][c.AuthorID] += fc.Additions + fc.Deletions
			if !c.AuthoredAt.Before(since) {
				alive[fc.Path] = true
			}
		}
	}
	owned := map[int64]int{}
	for path := range alive {
		var owner int64
		best := -1
		for aid, n := range lines[path] {
			if n > best || (n == best && aid < owner) {
				owner, best = aid, n
			}
		}
		owned[owner]++
	}
	total := len(alive)
	s.Summary.FilesTracked = total
	for aid, n := range owned {
		s.Ownership = append(s.Ownership, OwnerShare{AuthorID: aid, Name: ds.Authors[aid].Name, Files: n, Share: pct(n, total)})
	}
	sort.Slice(s.Ownership, func(i, j int) bool {
		if s.Ownership[i].Files != s.Ownership[j].Files {
			return s.Ownership[i].Files > s.Ownership[j].Files
		}
		return s.Ownership[i].AuthorID < s.Ownership[j].AuthorID
	})
	covered := 0
	for _, o := range s.Ownership {
		if total == 0 || covered*2 > total {
			break
		}
		covered += o.Files
		s.Summary.BusFactor++
	}
	return owned
}

func weekly(ds *domain.Dataset, p domain.Period, f Filter) []WeekPoint {
	start := WeekStart(p.From)
	var points []WeekPoint
	index := map[time.Time]int{}
	for w := start; w.Before(p.To); w = w.AddDate(0, 0, 7) {
		index[w] = len(points)
		points = append(points, WeekPoint{WeekStart: w})
	}
	authors := make([]map[int64]bool, len(points))
	leads := make([][]float64, len(points))
	at := func(t time.Time) int {
		if !p.Contains(t) {
			return -1
		}
		if i, ok := index[WeekStart(t)]; ok {
			return i
		}
		return -1
	}
	for _, c := range ds.Commits {
		i := at(c.AuthoredAt)
		if i < 0 || c.IsMerge || !f.has(c.AuthorID) {
			continue
		}
		points[i].Commits++
		points[i].Additions += c.Additions
		points[i].Deletions += c.Deletions
		if authors[i] == nil {
			authors[i] = map[int64]bool{}
		}
		authors[i][c.AuthorID] = true
	}
	for _, mr := range ds.MergeRequests {
		if mr.MergedAt == nil || !f.has(mr.AuthorID) {
			continue
		}
		if i := at(*mr.MergedAt); i >= 0 {
			points[i].MRMerged++
			leads[i] = append(leads[i], hours(mr.MergedAt.Sub(mr.CreatedAt)))
		}
	}
	for _, is := range ds.Issues {
		if is.ClosedAt == nil || (f != nil && !f.has(is.AssigneeID)) {
			continue
		}
		if i := at(*is.ClosedAt); i >= 0 {
			points[i].IssuesClosed++
		}
	}
	for i := range points {
		points[i].ActiveAuthors = len(authors[i])
		points[i].MRLeadMedianH = median(leads[i])
	}
	return points
}

// WeekStart возвращает понедельник 00:00 UTC недели, содержащей t.
func WeekStart(t time.Time) time.Time {
	t = t.UTC()
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

var (
	conventionalRe = regexp.MustCompile(`^(feat|fix|docs|style|refactor|perf|test|tests|build|ci|chore|revert)(\([^)]*\))?!?:\s*\S`)
	issueRefRe     = regexp.MustCompile(`(#\d+|\b[A-Z][A-Z0-9]+-\d+\b|![0-9]+)`)
	genericMsgs    = map[string]bool{"fix": true, "wip": true, "update": true, "updates": true, "changes": true,
		"change": true, "test": true, "tmp": true, "commit": true, "fixes": true, "minor": true, "правки": true,
		"фикс": true, "исправления": true, "изменения": true, ".": true, "-": true}
)

// GoodMessage — сообщение информативно: Conventional Commits или осмысленная тема
// длиной 10–100 символов, не из списка «пустых» (fix, wip, update…).
func GoodMessage(msg string) bool {
	subject := strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])
	if conventionalRe.MatchString(strings.ToLower(subject)) {
		return true
	}
	n := len([]rune(subject))
	if n < 10 || n > 100 {
		return false
	}
	return !genericMsgs[strings.ToLower(strings.Trim(subject, " .!"))]
}

// HasIssueRef — сообщение ссылается на задачу (#12, PROJ-34) или MR (!5).
func HasIssueRef(msg string) bool { return issueRefRe.MatchString(msg) }

func hours(d time.Duration) float64 { return d.Hours() }

func ptr(v float64) *float64 {
	v = math.Round(v*100) / 100
	return &v
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return math.Round(float64(n)/float64(d)*10000) / 100
}

// ratio — доля в процентах; nil, если знаменатель 0.
func ratio(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	return ptr(float64(n) / float64(d) * 100)
}

func ratioPlain(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	return ptr(float64(n) / float64(d))
}

func median(v []float64) *float64 { return percentile(v, 0.5) }

// percentile — линейная интерполяция между ближайшими рангами.
func percentile(v []float64, q float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return ptr(s[lo] + (s[hi]-s[lo])*(pos-float64(lo)))
}

func fmtID(id int64) string { return strconv.FormatInt(id, 10) }

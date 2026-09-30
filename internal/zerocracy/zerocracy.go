package zerocracy

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"teampulse/internal/domain"
	"teampulse/internal/metrics"
)

// Виды событий в журнале вклада.
const (
	KindMRMerged   = "mr_merged"
	KindMRSmall    = "mr_small"
	KindMRLarge    = "mr_large"
	KindMRNoReview = "mr_no_review"
	KindReview     = "review"
	KindIssue      = "issue_closed"
	KindCommit     = "commit_linked"
	KindOverdue    = "overdue"
)

// KindTitles — подписи событий для UI и отчётов.
var KindTitles = map[string]string{
	KindMRMerged: "Влитые MR", KindMRSmall: "Компактные MR", KindMRLarge: "Огромные MR",
	KindMRNoReview: "MR без ревью", KindReview: "Ревью", KindIssue: "Закрытые задачи",
	KindCommit: "Коммиты со ссылкой на задачу", KindOverdue: "Просроченные задачи",
}

// Event — запись журнала: кто, когда, за что и сколько баллов.
type Event struct {
	AuthorID int64     `json:"author_id"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Points   int       `json:"points"`
	Ref      string    `json:"ref"`
	Reason   string    `json:"reason"`
}

// Score — итог разработчика.
type Score struct {
	AuthorID  int64          `json:"author_id"`
	Name      string         `json:"name"`
	Points    int            `json:"points"`
	Breakdown map[string]int `json:"breakdown"`
	Share     float64        `json:"share"` // доля в положительных баллах команды, %
}

// Contribution — результат оценки вклада за период.
type Contribution struct {
	Scores []Score `json:"scores"`
	Events []Event `json:"events"`
	Total  int     `json:"total"`
	Policy Policy  `json:"policy"`
}

// Evaluate начисляет баллы за период по политике. Результат полностью
// воспроизводим: одни и те же данные всегда дают одни и те же баллы.
func Evaluate(ds *domain.Dataset, p domain.Period, f metrics.Filter, pol Policy) Contribution {
	var events []Event
	in := func(id int64) bool { return id != 0 && (f == nil || f[id]) }
	emit := func(aid int64, at time.Time, kind string, pts int, ref, reason string) {
		if pts != 0 {
			events = append(events, Event{aid, at, kind, pts, ref, reason})
		}
	}

	for _, mr := range ds.MergeRequests {
		ref := fmt.Sprintf("!%d", mr.IID)
		if mr.MergedAt != nil && p.Contains(*mr.MergedAt) && in(mr.AuthorID) {
			at := *mr.MergedAt
			emit(mr.AuthorID, at, KindMRMerged, pol.MRMerged, ref, "MR влит: "+mr.Title)
			if mr.Additions >= 0 {
				size := mr.Additions + mr.Deletions
				switch {
				case size <= pol.MRSmallLines:
					emit(mr.AuthorID, at, KindMRSmall, pol.MRSmallBonus, ref, fmt.Sprintf("компактный MR (%d строк)", size))
				case size > pol.MRLargeLines:
					emit(mr.AuthorID, at, KindMRLarge, pol.MRLargePenalty, ref, fmt.Sprintf("огромный MR (%d строк)", size))
				}
			}
			if len(mr.Reviews) == 0 {
				emit(mr.AuthorID, at, KindMRNoReview, pol.MRNoReviewPenalty, ref, "влит без ревью")
			}
		}
		for _, r := range mr.Reviews {
			if p.Contains(r.At) && in(r.AuthorID) {
				pts := pol.Review + min(r.Comments, pol.ReviewCommentCap)*pol.ReviewComment
				emit(r.AuthorID, r.At, KindReview, pts, ref, fmt.Sprintf("ревью (%d комм.)", r.Comments))
			}
		}
	}

	for _, is := range ds.Issues {
		ref := fmt.Sprintf("#%d", is.IID)
		if is.ClosedAt != nil && p.Contains(*is.ClosedAt) && in(is.AssigneeID) {
			pts, why := issuePoints(is, pol)
			emit(is.AssigneeID, *is.ClosedAt, KindIssue, pts, ref, "задача закрыта: "+is.Title+why)
		}
		openAtEnd := is.ClosedAt == nil || !is.ClosedAt.Before(p.To)
		age := p.To.Sub(is.CreatedAt)
		if openAtEnd && in(is.AssigneeID) && is.CreatedAt.Before(p.To) && age > time.Duration(pol.OverdueDays)*24*time.Hour {
			emit(is.AssigneeID, p.To.Add(-time.Second), KindOverdue, pol.OverduePenalty, ref,
				fmt.Sprintf("задача открыта %d дн. (SLA %d)", int(age.Hours()/24), pol.OverdueDays))
		}
	}

	perDay := map[string]int{}
	for _, c := range ds.Commits {
		if c.IsMerge || !p.Contains(c.AuthoredAt) || !in(c.AuthorID) || !metrics.HasIssueRef(c.Message) {
			continue
		}
		key := fmt.Sprintf("%d|%s", c.AuthorID, c.LocalTime().Format("2006-01-02"))
		if perDay[key] >= pol.CommitLinkedPerDay {
			continue
		}
		perDay[key]++
		subject := strings.SplitN(c.Message, "\n", 2)[0]
		emit(c.AuthorID, c.AuthoredAt, KindCommit, pol.CommitLinked, c.SHA[:min(8, len(c.SHA))], subject)
	}

	sort.Slice(events, func(i, j int) bool {
		if !events[i].At.Equal(events[j].At) {
			return events[i].At.After(events[j].At)
		}
		return events[i].Ref < events[j].Ref
	})

	byAuthor := map[int64]*Score{}
	positive := 0
	res := Contribution{Policy: pol, Events: events}
	for _, e := range events {
		s := byAuthor[e.AuthorID]
		if s == nil {
			s = &Score{AuthorID: e.AuthorID, Name: ds.Authors[e.AuthorID].Name, Breakdown: map[string]int{}}
			byAuthor[e.AuthorID] = s
		}
		s.Points += e.Points
		s.Breakdown[e.Kind] += e.Points
		res.Total += e.Points
	}
	for _, s := range byAuthor {
		positive += max(s.Points, 0)
	}
	for _, s := range byAuthor {
		if positive > 0 {
			s.Share = math.Round(float64(max(s.Points, 0))/float64(positive)*1000) / 10
		}
		res.Scores = append(res.Scores, *s)
	}
	sort.Slice(res.Scores, func(i, j int) bool {
		if res.Scores[i].Points != res.Scores[j].Points {
			return res.Scores[i].Points > res.Scores[j].Points
		}
		return res.Scores[i].Name < res.Scores[j].Name
	})
	return res
}

// issuePoints — баллы за задачу: максимум по оценочным меткам, иначе базовые.
func issuePoints(is domain.Issue, pol Policy) (int, string) {
	best, label := 0, ""
	for _, l := range is.Labels {
		if v, ok := pol.LabelPoints[strings.ToLower(l)]; ok && v > best {
			best, label = v, l
		}
	}
	if best == 0 {
		return pol.IssueClosed, ""
	}
	return best, " [" + label + "]"
}

// Candidate — кандидат на задачу.
type Candidate struct {
	AuthorID  int64   `json:"author_id"`
	Name      string  `json:"name"`
	Login     string  `json:"login,omitempty"`
	Score     float64 `json:"score"`
	Expertise float64 `json:"expertise"` // 0..1 относительно лучшего кандидата
	Load      int     `json:"load"`      // открытых назначенных задач
	Reason    string  `json:"reason"`
}

// Suggestion — рекомендация исполнителя для неназначенной задачи.
type Suggestion struct {
	IssueIID   int         `json:"issue_iid"`
	Title      string      `json:"title"`
	Labels     []string    `json:"labels"`
	Candidates []Candidate `json:"candidates"`
}

// Веса скоринга: экспертиза важнее, но перегруженного человека система обходит.
const (
	expertiseWeight = 0.6
	loadWeight      = 0.4
	activeWindow    = 90 * 24 * time.Hour
)

// Suggest подбирает исполнителей для открытых неназначенных задач:
// экспертиза (пути файлов и метки прошлых задач, пересекающиеся с задачей) и текущая загрузка.
func Suggest(ds *domain.Dataset, now time.Time, f metrics.Filter, limit int) []Suggestion {
	affinity := map[int64]map[string]float64{}
	add := func(aid int64, token string, w float64) {
		if affinity[aid] == nil {
			affinity[aid] = map[string]float64{}
		}
		affinity[aid][token] += w
	}
	for _, c := range ds.Commits {
		if c.IsMerge || now.Sub(c.AuthoredAt) > activeWindow || (f != nil && !f[c.AuthorID]) {
			continue
		}
		for _, fc := range c.Files {
			if fc.Excluded {
				continue
			}
			w := math.Log1p(float64(fc.Additions + fc.Deletions))
			for _, t := range tokens(fc.Path) {
				add(c.AuthorID, t, w)
			}
		}
	}
	load := map[int64]int{}
	for _, is := range ds.Issues {
		if is.AssigneeID == 0 {
			continue
		}
		if is.ClosedAt == nil {
			load[is.AssigneeID]++
		} else if _, ok := affinity[is.AssigneeID]; ok {
			for _, l := range is.Labels {
				add(is.AssigneeID, strings.ToLower(l), 3)
			}
			for _, t := range tokens(is.Title) {
				add(is.AssigneeID, t, 1)
			}
		}
	}
	if len(affinity) == 0 {
		return nil
	}
	maxLoad := 0
	for aid := range affinity {
		maxLoad = max(maxLoad, load[aid])
	}

	var out []Suggestion
	for _, is := range ds.Issues {
		if is.ClosedAt != nil || is.AssigneeID != 0 {
			continue
		}
		want := tokens(is.Title)
		for _, l := range is.Labels {
			want = append(want, strings.ToLower(l))
		}
		raw := map[int64]float64{}
		matched := map[int64][]string{}
		best := 0.0
		for aid, aff := range affinity {
			for _, t := range want {
				if v := aff[t]; v > 0 {
					raw[aid] += v
					matched[aid] = append(matched[aid], t)
				}
			}
			best = max(best, raw[aid])
		}
		var cands []Candidate
		for aid := range affinity {
			exp := 0.0
			if best > 0 {
				exp = raw[aid] / best
			}
			loadScore := 1 - float64(load[aid])/float64(maxLoad+1)
			a := ds.Authors[aid]
			reason := fmt.Sprintf("загрузка: %d открытых задач", load[aid])
			if len(matched[aid]) > 0 {
				reason = "опыт: " + strings.Join(uniq(matched[aid], 4), ", ") + "; " + reason
			}
			cands = append(cands, Candidate{
				AuthorID: aid, Name: a.Name, Login: a.Login, Load: load[aid], Reason: reason,
				Expertise: math.Round(exp*100) / 100,
				Score:     math.Round((expertiseWeight*exp+loadWeight*loadScore)*100) / 100,
			})
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].Score != cands[j].Score {
				return cands[i].Score > cands[j].Score
			}
			return cands[i].AuthorID < cands[j].AuthorID
		})
		if len(cands) > 3 {
			cands = cands[:3]
		}
		out = append(out, Suggestion{IssueIID: is.IID, Title: is.Title, Labels: is.Labels, Candidates: cands})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "into": true, "add": true, "fix": true,
	"src": true, "internal": true, "pkg": true, "lib": true, "app": true, "main": true, "index": true, "test": true,
	"tests": true, "для": true, "при": true, "как": true, "что": true, "добавить": true, "исправить": true, "ошибка": true,
	"go": true, "js": true, "ts": true, "py": true, "java": true, "md": true, "json": true, "yaml": true, "yml": true,
}

// tokens разбивает путь или заголовок на значимые слова (≥ 3 символов, без стоп-слов).
func tokens(s string) []string {
	parts := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var out []string
	for _, p := range parts {
		if len([]rune(p)) >= 3 && !stopWords[p] {
			out = append(out, p)
		}
	}
	return out
}

func uniq(s []string, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
			if len(out) == limit {
				break
			}
		}
	}
	return out
}

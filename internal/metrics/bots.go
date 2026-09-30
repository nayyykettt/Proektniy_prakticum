package metrics

import (
	"strings"

	"teampulse/internal/domain"
)

var botMarkers = []string{"[bot]", "dependabot", "renovate", "github-actions", "gitlab-bot", "snyk-bot", "greenkeeper", "semantic-release-bot"}

// IsBot распознаёт автоматические учётные записи (обновление зависимостей, CI).
// Их активность не является работой команды и искажает метрики.
func IsBot(a domain.Author) bool {
	s := strings.ToLower(a.Name + " " + a.Email + " " + a.Login)
	for _, m := range botMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// WithoutBots возвращает набор данных без активности ботов.
func WithoutBots(ds *domain.Dataset) *domain.Dataset {
	bots := map[int64]bool{}
	for id, a := range ds.Authors {
		if IsBot(a) {
			bots[id] = true
		}
	}
	if len(bots) == 0 {
		return ds
	}
	out := &domain.Dataset{Authors: ds.Authors, Issues: ds.Issues}
	for _, c := range ds.Commits {
		if !bots[c.AuthorID] {
			out.Commits = append(out.Commits, c)
		}
	}
	for _, mr := range ds.MergeRequests {
		if bots[mr.AuthorID] {
			continue
		}
		reviews := mr.Reviews[:0:0]
		for _, r := range mr.Reviews {
			if !bots[r.AuthorID] {
				reviews = append(reviews, r)
			}
		}
		mr.Reviews = reviews
		out.MergeRequests = append(out.MergeRequests, mr)
	}
	return out
}

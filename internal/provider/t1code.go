package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"teampulse/internal/domain"
)

// T1Code — клиент REST API Т1 Код (Сфера.Код). Платформа совместима с GitLab API v4,
// поэтому клиент работает и с gitlab.com / self-hosted GitLab.
type T1Code struct {
	base       string // https://host/api/v4
	project    string // URL-экранированный ID или путь group/project
	token      string
	http       *http.Client
	maxDetails int           // лимит MR, для которых запрашиваются комментарии и diff
	workers    int           // параллельные запросы деталей MR
	backoff    time.Duration // базовая пауза перед повтором
}

// NewT1Code создаёт клиент. baseURL — адрес инстанса (https://code.example.ru).
func NewT1Code(baseURL, projectID, token string, client *http.Client) (*T1Code, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	baseURL = strings.TrimSuffix(baseURL, "/api/v4")
	if baseURL == "" || strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("%w: для Т1 Код нужны адрес инстанса и ID проекта", domain.ErrInvalid)
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &T1Code{
		base:       baseURL + "/api/v4",
		project:    url.PathEscape(strings.Trim(projectID, "/")),
		token:      token,
		http:       client,
		maxDetails: 500,
		workers:    4,
		backoff:    time.Second,
	}, nil
}

// APIError — ошибка ответа API с понятным описанием.
type APIError struct {
	Status int
	Msg    string
}

func (e *APIError) Error() string { return fmt.Sprintf("Т1 Код: %s (HTTP %d)", e.Msg, e.Status) }

type apiUser struct {
	Username string `json:"username"`
	Name     string `json:"name"`
}

type apiMR struct {
	IID       int        `json:"iid"`
	Title     string     `json:"title"`
	State     string     `json:"state"`
	CreatedAt time.Time  `json:"created_at"`
	MergedAt  *time.Time `json:"merged_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	Author    apiUser    `json:"author"`
}

type apiNote struct {
	Body      string    `json:"body"`
	System    bool      `json:"system"`
	CreatedAt time.Time `json:"created_at"`
	Author    apiUser   `json:"author"`
}

type apiIssue struct {
	IID       int        `json:"iid"`
	Title     string     `json:"title"`
	Labels    []string   `json:"labels"`
	State     string     `json:"state"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at"`
	Author    apiUser    `json:"author"`
	Assignees []apiUser  `json:"assignees"`
	Assignee  *apiUser   `json:"assignee"`
}

// FetchMergeRequests загружает MR и для каждого — ревью (комментарии, approve) и размер diff.
func (c *T1Code) FetchMergeRequests(ctx context.Context, since time.Time) ([]domain.MergeRequest, error) {
	q := url.Values{"state": {"all"}, "scope": {"all"}, "order_by": {"updated_at"}, "sort": {"desc"}}
	if !since.IsZero() {
		q.Set("updated_after", since.UTC().Format(time.RFC3339))
	}
	var raw []apiMR
	if err := c.paginate(ctx, "/projects/"+c.project+"/merge_requests", q, func(dec *json.Decoder) error {
		var page []apiMR
		if err := dec.Decode(&page); err != nil {
			return err
		}
		raw = append(raw, page...)
		return nil
	}); err != nil {
		return nil, err
	}

	out := make([]domain.MergeRequest, len(raw))
	for i, m := range raw {
		state := m.State
		if state == "locked" {
			state = "opened"
		}
		out[i] = domain.MergeRequest{
			IID: m.IID, Title: m.Title, State: state, CreatedAt: m.CreatedAt.UTC(),
			MergedAt: utc(m.MergedAt), ClosedAt: utc(m.ClosedAt),
			AuthorLogin: m.Author.Username, AuthorName: m.Author.Name,
			Additions: -1, Deletions: -1,
		}
		if state == "merged" && out[i].MergedAt == nil { // старые версии API
			out[i].MergedAt = utc(m.ClosedAt)
		}
	}

	// Детали — параллельно ограниченным пулом, чтобы не упираться в rate limit.
	n := min(len(out), c.maxDetails)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	sem := make(chan struct{}, c.workers)
	for i := range n {
		wg.Add(1)
		sem <- struct{}{}
		go func(mr *domain.MergeRequest) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := c.fillDetails(ctx, mr); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(&out[i])
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (c *T1Code) fillDetails(ctx context.Context, mr *domain.MergeRequest) error {
	path := fmt.Sprintf("/projects/%s/merge_requests/%d", c.project, mr.IID)
	reviews := map[string]*domain.Review{}
	var order []string
	err := c.paginate(ctx, path+"/notes", url.Values{"sort": {"asc"}, "order_by": {"created_at"}}, func(dec *json.Decoder) error {
		var notes []apiNote
		if err := dec.Decode(&notes); err != nil {
			return err
		}
		for _, n := range notes {
			approve := n.System && strings.HasPrefix(strings.ToLower(n.Body), "approved")
			if (n.System && !approve) || n.Author.Username == "" || n.Author.Username == mr.AuthorLogin {
				continue
			}
			r := reviews[n.Author.Username]
			if r == nil {
				r = &domain.Review{AuthorLogin: n.Author.Username, AuthorName: n.Author.Name, At: n.CreatedAt.UTC()}
				reviews[n.Author.Username] = r
				order = append(order, n.Author.Username)
			}
			if !approve {
				r.Comments++
				mr.Comments++
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, login := range order {
		r := *reviews[login]
		mr.Reviews = append(mr.Reviews, r)
		if mr.FirstReviewAt == nil || r.At.Before(*mr.FirstReviewAt) {
			at := r.At
			mr.FirstReviewAt = &at
		}
	}
	if mr.State != "merged" {
		return nil // размер нужен только для влитых MR; открытые догрузятся после влития
	}
	add, del, err := c.diffSize(ctx, path)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusForbidden) {
			return nil // размер неизвестен — не критично
		}
		return err
	}
	mr.Additions, mr.Deletions = add, del
	return nil
}

// diffSize считает строки +/- по unified diff MR.
func (c *T1Code) diffSize(ctx context.Context, mrPath string) (int, int, error) {
	var add, del int
	err := c.paginate(ctx, mrPath+"/diffs", nil, func(dec *json.Decoder) error {
		var diffs []struct {
			Diff string `json:"diff"`
		}
		if err := dec.Decode(&diffs); err != nil {
			return err
		}
		for _, d := range diffs {
			a, r := countDiff(d.Diff)
			add += a
			del += r
		}
		return nil
	})
	return add, del, err
}

func countDiff(diff string) (add, del int) {
	sc := bufio.NewScanner(strings.NewReader(diff))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			add++
		case strings.HasPrefix(line, "-"):
			del++
		}
	}
	return add, del
}

// FetchIssues загружает задачи проекта.
func (c *T1Code) FetchIssues(ctx context.Context, since time.Time) ([]domain.Issue, error) {
	q := url.Values{"state": {"all"}, "scope": {"all"}, "order_by": {"updated_at"}}
	if !since.IsZero() {
		q.Set("updated_after", since.UTC().Format(time.RFC3339))
	}
	var out []domain.Issue
	err := c.paginate(ctx, "/projects/"+c.project+"/issues", q, func(dec *json.Decoder) error {
		var page []apiIssue
		if err := dec.Decode(&page); err != nil {
			return err
		}
		for _, is := range page {
			d := domain.Issue{IID: is.IID, Title: is.Title, Labels: is.Labels, State: is.State,
				AuthorLogin: is.Author.Username, CreatedAt: is.CreatedAt.UTC(), ClosedAt: utc(is.ClosedAt)}
			assignee := is.Assignee
			if len(is.Assignees) > 0 {
				assignee = &is.Assignees[0]
			}
			if assignee != nil {
				d.AssigneeLogin, d.AssigneeName = assignee.Username, assignee.Name
			}
			out = append(out, d)
		}
		return nil
	})
	return out, err
}

// AssignIssue назначает задачу пользователю (Zerocracy: автоматическое распределение).
func (c *T1Code) AssignIssue(ctx context.Context, iid int, login string) error {
	var users []struct {
		ID int64 `json:"id"`
	}
	resp, err := c.do(ctx, http.MethodGet, "/users", url.Values{"username": {login}}, nil)
	if err != nil {
		return err
	}
	err = json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if len(users) == 0 {
		return fmt.Errorf("%w: пользователь %q не найден в Т1 Код", domain.ErrNotFound, login)
	}
	body := strings.NewReader(fmt.Sprintf(`{"assignee_ids":[%d]}`, users[0].ID))
	resp, err = c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%s/issues/%d", c.project, iid), nil, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// paginate обходит страницы по заголовку X-Next-Page.
func (c *T1Code) paginate(ctx context.Context, path string, q url.Values, fn func(*json.Decoder) error) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("per_page", "100")
	page := "1"
	for i := 0; page != "" && i < 1000; i++ {
		q.Set("page", page)
		resp, err := c.do(ctx, http.MethodGet, path, q, nil)
		if err != nil {
			return err
		}
		err = fn(json.NewDecoder(resp.Body))
		resp.Body.Close()
		if err != nil {
			return fmt.Errorf("Т1 Код: неожиданный формат ответа %s: %w", path, err)
		}
		page = resp.Header.Get("X-Next-Page")
	}
	return nil
}

// do выполняет запрос с повторами при 429/5xx и сетевых сбоях.
func (c *T1Code) do(ctx context.Context, method, path string, q url.Values, body io.ReadSeeker) (*http.Response, error) {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	const attempts = 4
	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			wait := c.backoff << (attempt - 1)
			if ra, ok := lastErr.(*retryAfter); ok && ra.d > 0 {
				wait = min(ra.d, 30*time.Second)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
			if body != nil {
				_, _ = body.Seek(0, io.SeekStart)
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return nil, err
		}
		if c.token != "" { // публичные проекты читаются и без токена
			req.Header.Set("PRIVATE-TOKEN", c.token)
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = fmt.Errorf("Т1 Код недоступен: %w", err)
			continue
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			lastErr = &retryAfter{d: time.Duration(secs) * time.Second, status: resp.StatusCode}
			continue
		}
		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, &APIError{Status: resp.StatusCode, Msg: statusText(resp.StatusCode)}
		}
		return resp, nil
	}
	if ra, ok := lastErr.(*retryAfter); ok {
		return nil, &APIError{Status: ra.status, Msg: "сервис перегружен или недоступен, повторите позже"}
	}
	return nil, lastErr
}

type retryAfter struct {
	d      time.Duration
	status int
}

func (r *retryAfter) Error() string { return "retry" }

func statusText(code int) string {
	switch code {
	case http.StatusUnauthorized:
		return "нужен действующий токен доступа (read_api): токен не задан, неверен или просрочен"
	case http.StatusForbidden:
		return "у токена недостаточно прав (нужен scope read_api)"
	case http.StatusNotFound:
		return "проект не найден — проверьте ID/путь проекта"
	}
	return "ошибка запроса"
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

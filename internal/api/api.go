// Package api — REST API сервиса (JSON).
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"teampulse/internal/app"
	"teampulse/internal/domain"
	"teampulse/internal/httpx"
	"teampulse/internal/metrics"
	"teampulse/internal/report"
)

// Handler — обработчики API.
type Handler struct {
	App  *app.App
	Auth *httpx.Auth
}

// Register подключает маршруты API к mux.
func (h *Handler) Register(mux *http.ServeMux) {
	// Каждый маршрут с {id} или {rid} проверяет, что проект принадлежит пользователю.
	route := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, h.Auth.API(h.owned(fn)))
	}
	route("GET /api/projects", h.listProjects)
	route("POST /api/projects", h.createProject)
	route("GET /api/projects/{id}", h.getProject)
	route("PUT /api/projects/{id}", h.updateProject)
	route("DELETE /api/projects/{id}", h.deleteProject)
	route("POST /api/projects/{id}/sync", h.sync)

	route("GET /api/projects/{id}/metrics", h.metrics)
	route("GET /api/projects/{id}/trends", h.trends)
	route("GET /api/projects/{id}/recommendations", h.recommendations)
	route("GET /api/projects/{id}/contribution", h.contribution)
	route("GET /api/projects/{id}/assignments", h.assignments)

	route("GET /api/projects/{id}/authors", h.listAuthors)
	route("GET /api/projects/{id}/authors/{aid}", h.author)
	route("POST /api/projects/{id}/authors/merge", h.mergeAuthors)

	route("GET /api/projects/{id}/teams", h.listTeams)
	route("POST /api/projects/{id}/teams", h.saveTeam)
	route("DELETE /api/projects/{id}/teams/{tid}", h.deleteTeam)

	route("GET /api/projects/{id}/reports", h.listReports)
	route("POST /api/projects/{id}/reports", h.createReport)
	route("GET /api/reports/{rid}", h.getReport)

	route("GET /api/metrics/definitions", h.definitions)
}

func (h *Handler) owned(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := CheckAccess(r, h.App); err != nil {
			h.fail(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckAccess проверяет доступ к проекту {id} и отчёту {rid} из пути запроса.
// Чужой проект неотличим от несуществующего (404), чтобы не раскрывать его наличие.
func CheckAccess(r *http.Request, a *app.App) error {
	var projectID int64
	if r.PathValue("rid") != "" {
		rid, err := PathID(r, "rid")
		if err != nil {
			return err
		}
		rep, err := a.Store.GetReport(r.Context(), rid)
		if err != nil {
			return err
		}
		projectID = rep.ProjectID
	}
	if r.PathValue("id") != "" {
		id, err := PathID(r, "id")
		if err != nil {
			return err
		}
		projectID = id
	}
	if projectID == 0 {
		return nil
	}
	p, err := a.Store.GetProject(r.Context(), projectID)
	if err != nil {
		return err
	}
	if !httpx.CanAccess(r, p) {
		return domain.ErrNotFound
	}
	return nil
}

// ListProjects — проекты, доступные текущему запросу.
func ListProjects(r *http.Request, a *app.App) ([]domain.Project, error) {
	if httpx.IsService(r) {
		return a.Store.ListProjects(r.Context())
	}
	return a.Store.ListUserProjects(r.Context(), httpx.User(r).ID)
}

func (h *Handler) fail(w http.ResponseWriter, err error) { httpx.Fail(w, h.App.Log, err) }

// PathID разбирает числовой параметр пути.
func PathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: некорректный идентификатор %s", domain.ErrInvalid, name)
	}
	return id, nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: некорректный JSON: %v", domain.ErrInvalid, err)
	}
	return nil
}

func (h *Handler) listProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := ListProjects(r, h.App)
	if err != nil {
		h.fail(w, err)
		return
	}
	if ps == nil {
		ps = []domain.Project{}
	}
	httpx.JSON(w, http.StatusOK, ps)
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	var in app.ProjectInput
	if err := decode(r, &in); err != nil {
		h.fail(w, err)
		return
	}
	p, err := h.App.CreateProject(r.Context(), httpx.User(r).ID, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, p)
}

func (h *Handler) getProject(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	p, err := h.App.Store.GetProject(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (h *Handler) updateProject(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	var in app.ProjectInput
	if err := decode(r, &in); err != nil {
		h.fail(w, err)
		return
	}
	p, err := h.App.UpdateProject(r.Context(), id, in)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, p)
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := h.App.DeleteProject(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	if r.URL.Query().Get("wait") == "true" {
		res, err := h.App.Sync(r.Context(), id)
		if err != nil {
			h.fail(w, err)
			return
		}
		httpx.JSON(w, http.StatusOK, res)
		return
	}
	if err := h.App.SyncAsync(id); err != nil {
		h.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, map[string]string{"status": domain.SyncRunning})
}

// analyze — общий разбор параметров ?from=&to=&team= и расчёт.
func (h *Handler) analyze(w http.ResponseWriter, r *http.Request, scope app.Scope) (*report.Data, bool) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return nil, false
	}
	q := r.URL.Query()
	period, err := app.ParsePeriod(q.Get("from"), q.Get("to"), time.Now())
	if err != nil {
		h.fail(w, err)
		return nil, false
	}
	if t := q.Get("team"); t != "" && scope.AuthorID == 0 {
		if scope.TeamID, err = strconv.ParseInt(t, 10, 64); err != nil {
			h.fail(w, fmt.Errorf("%w: некорректный team", domain.ErrInvalid))
			return nil, false
		}
	}
	d, err := h.App.Analyze(r.Context(), id, period, scope)
	if err != nil {
		h.fail(w, err)
		return nil, false
	}
	return d, true
}

func (h *Handler) metrics(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.analyze(w, r, app.Scope{}); ok {
		httpx.JSON(w, http.StatusOK, d.Snapshot)
	}
}

func (h *Handler) trends(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.analyze(w, r, app.Scope{}); ok {
		httpx.JSON(w, http.StatusOK, d.Trends)
	}
}

func (h *Handler) recommendations(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.analyze(w, r, app.Scope{}); ok {
		httpx.JSON(w, http.StatusOK, d.Recommendations)
	}
}

func (h *Handler) contribution(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.analyze(w, r, app.Scope{}); ok {
		httpx.JSON(w, http.StatusOK, d.Contribution)
	}
}

func (h *Handler) assignments(w http.ResponseWriter, r *http.Request) {
	if d, ok := h.analyze(w, r, app.Scope{}); ok {
		out := d.Suggestions
		if out == nil {
			httpx.JSON(w, http.StatusOK, []any{})
			return
		}
		httpx.JSON(w, http.StatusOK, out)
	}
}

func (h *Handler) listAuthors(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	if _, err := h.App.Store.GetProject(r.Context(), id); err != nil {
		h.fail(w, err)
		return
	}
	as, err := h.App.Store.ListAuthors(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if as == nil {
		as = []domain.Author{}
	}
	httpx.JSON(w, http.StatusOK, as)
}

func (h *Handler) author(w http.ResponseWriter, r *http.Request) {
	aid, err := PathID(r, "aid")
	if err != nil {
		h.fail(w, err)
		return
	}
	if d, ok := h.analyze(w, r, app.Scope{AuthorID: aid}); ok {
		httpx.JSON(w, http.StatusOK, d)
	}
}

func (h *Handler) mergeAuthors(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	var in struct {
		TargetID int64 `json:"target_id"`
		SourceID int64 `json:"source_id"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, err)
		return
	}
	if err := h.App.Store.MergeAuthors(r.Context(), id, in.TargetID, in.SourceID); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listTeams(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	ts, err := h.App.Store.ListTeams(r.Context(), id)
	if err != nil {
		h.fail(w, err)
		return
	}
	if ts == nil {
		ts = []domain.Team{}
	}
	httpx.JSON(w, http.StatusOK, ts)
}

func (h *Handler) saveTeam(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	var t domain.Team
	if err := decode(r, &t); err != nil {
		h.fail(w, err)
		return
	}
	t.ProjectID = id
	if err := h.App.Store.SaveTeam(r.Context(), &t); err != nil {
		h.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusOK, t)
}

func (h *Handler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	tid, err := PathID(r, "tid")
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := h.App.Store.DeleteTeam(r.Context(), id, tid); err != nil {
		h.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listReports(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	rs, err := h.App.Store.ListReports(r.Context(), id, 100)
	if err != nil {
		h.fail(w, err)
		return
	}
	if rs == nil {
		rs = []domain.StoredReport{}
	}
	httpx.JSON(w, http.StatusOK, rs)
}

func (h *Handler) createReport(w http.ResponseWriter, r *http.Request) {
	id, err := PathID(r, "id")
	if err != nil {
		h.fail(w, err)
		return
	}
	var in struct {
		From   string `json:"from"`
		To     string `json:"to"`
		TeamID int64  `json:"team_id"`
	}
	if err := decode(r, &in); err != nil {
		h.fail(w, err)
		return
	}
	period, err := app.ParsePeriod(in.From, in.To, time.Now())
	if err != nil {
		h.fail(w, err)
		return
	}
	rep, err := h.App.GenerateReport(r.Context(), id, period, in.TeamID, "manual")
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.JSON(w, http.StatusCreated, rep)
}

func (h *Handler) getReport(w http.ResponseWriter, r *http.Request) {
	rid, err := PathID(r, "rid")
	if err != nil {
		h.fail(w, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}
	if err := ServeReport(w, r, h.App, rid, format); err != nil {
		h.fail(w, err)
	}
}

// ServeReport отдаёт сохранённый отчёт в запрошенном формате как файл.
func ServeReport(w http.ResponseWriter, r *http.Request, a *app.App, rid int64, format string) error {
	ctype, ok := report.Formats[format]
	if !ok {
		return fmt.Errorf("%w: формат должен быть html, md, csv или json", domain.ErrInvalid)
	}
	d, _, err := a.LoadReport(r.Context(), rid)
	if err != nil {
		return err
	}
	body, err := report.Bytes(d, format)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", ctype)
	if format == "html" && r.URL.Query().Get("download") != "1" {
		// Самодостаточный HTML со встроенными стилями: разрешаем только их.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:")
	} else {
		// filename* (RFC 6266) — корректная передача кириллицы в имени файла.
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="report.%s"; filename*=UTF-8''%s`,
			format, url.PathEscape(report.Filename(d, format))))
	}
	_, err = w.Write(body)
	return err
}

func (h *Handler) definitions(w http.ResponseWriter, _ *http.Request) {
	httpx.JSON(w, http.StatusOK, metrics.Definitions)
}

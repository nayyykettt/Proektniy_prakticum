// Package web — серверный рендеринг интерфейса (html/template + Chart.js).
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"teampulse/internal/analysis"
	"teampulse/internal/api"
	"teampulse/internal/app"
	"teampulse/internal/domain"
	"teampulse/internal/httpx"
	"teampulse/internal/metrics"
	"teampulse/internal/report"
	"teampulse/internal/zerocracy"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Handler — обработчики страниц.
type Handler struct {
	App   *app.App
	Auth  *httpx.Auth
	pages map[string]*template.Template
}

// New разбирает шаблоны (ошибка шаблона — ошибка старта, а не рантайма).
func New(a *app.App, auth *httpx.Auth) (*Handler, error) {
	h := &Handler{App: a, Auth: auth, pages: map[string]*template.Template{}}
	names, err := fs.Glob(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		name := strings.TrimPrefix(n, "templates/")
		if name == "layout.html" {
			continue
		}
		t, err := template.New(name).Funcs(funcs).ParseFS(templatesFS, "templates/layout.html", n)
		if err != nil {
			return nil, fmt.Errorf("шаблон %s: %w", name, err)
		}
		h.pages[name] = t
	}
	return h, nil
}

// Register подключает маршруты страниц.
func (h *Handler) Register(mux *http.ServeMux) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheStatic(http.FileServerFS(static))))
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("GET /register", h.registerPage)
	mux.HandleFunc("POST /register", h.register)
	mux.HandleFunc("POST /logout", h.logout)

	// Каждая страница с {id} или {rid} проверяет, что проект принадлежит пользователю.
	page := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, h.Auth.Web(h.owned(fn)))
	}
	page("GET /{$}", h.index)
	page("POST /projects", h.createProject)
	page("GET /projects/{id}", h.dashboard)
	page("POST /projects/{id}/sync", h.sync)
	page("GET /projects/{id}/authors/{aid}", h.author)
	page("GET /projects/{id}/reports", h.reports)
	page("POST /projects/{id}/reports", h.createReport)
	page("GET /reports/{rid}", h.report)
	page("GET /projects/{id}/settings", h.settings)
	page("POST /projects/{id}/settings", h.saveSettings)
	page("POST /projects/{id}/delete", h.deleteProject)
	page("POST /projects/{id}/teams", h.saveTeam)
	page("POST /projects/{id}/teams/{tid}/delete", h.deleteTeam)
	page("POST /projects/{id}/authors/merge", h.mergeAuthors)
}

// assetVersions — короткий хеш содержимого каждого статического файла. Ссылки вида
// /static/app.css?v=<хеш> меняются при каждой правке, поэтому браузер не держит устаревший кэш.
var assetVersions = func() map[string]string {
	m := map[string]string{}
	_ = fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		m[strings.TrimPrefix(p, "static/")] = hex.EncodeToString(sum[:4])
		return nil
	})
	return m
}()

// asset возвращает URL статического файла с версией по содержимому.
func asset(name string) string {
	if v, ok := assetVersions[name]; ok {
		return "/static/" + name + "?v=" + v
	}
	return "/static/" + name
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) owned(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := api.CheckAccess(r, h.App); err != nil {
			h.fail(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Base — общие данные всех страниц.
type Base struct {
	Title   string
	Accent  string // цвет интерфейса: sky | blue | green
	User    domain.User
	CSRF    string
	Flash   string
	Error   string
	Project *domain.Project
	Tab     string
	Refresh bool // автообновление страницы во время синхронизации
}

func (h *Handler) base(r *http.Request, title string) Base {
	return Base{
		Title: title, Accent: accentOf(r), User: httpx.User(r),
		CSRF: h.Auth.CSRFToken(r), Flash: flashes[r.URL.Query().Get("msg")],
	}
}

// Accents — цвета интерфейса (первый — по умолчанию). Выбор хранится в cookie браузера.
var Accents = []struct{ ID, Name string }{{"sky", "Голубой"}, {"blue", "Синий"}, {"green", "Зелёный"}}

func accentOf(r *http.Request) string {
	if c, err := r.Cookie("tp_accent"); err == nil {
		for _, a := range Accents {
			if a.ID == c.Value {
				return a.ID
			}
		}
	}
	return Accents[0].ID
}

// Сообщения об успехе передаются кодом, а не текстом — ничего не отражается из URL.
var flashes = map[string]string{
	"created": "Проект создан. Запущена первая синхронизация.", "sync": "Синхронизация запущена.",
	"saved": "Изменения сохранены.", "deleted": "Проект удалён.", "report": "Отчёт сформирован.",
	"team": "Команда сохранена.", "team_deleted": "Команда удалена.", "merged": "Авторы объединены.",
	"registered": "Аккаунт создан. Войдите с логином и паролем.",
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, name string, data any) {
	t, ok := h.pages[name]
	if !ok {
		http.Error(w, "шаблон не найден", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		h.App.Log.Error("рендеринг шаблона", "page", name, "err", err)
		http.Error(w, "Ошибка отображения страницы", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (h *Handler) errorPage(w http.ResponseWriter, r *http.Request, status int, msg string) {
	b := h.base(r, "Ошибка")
	b.Error = msg
	h.render(w, r, status, "error.html", struct{ Base }{b})
}

// fail показывает ошибку прикладного слоя пользователю.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := httpx.StatusOf(err)
	if status >= 500 {
		h.App.Log.Error("ошибка страницы", "path", r.URL.Path, "err", err)
	}
	h.errorPage(w, r, status, msg)
}

func (h *Handler) project(w http.ResponseWriter, r *http.Request) (*domain.Project, bool) {
	id, err := api.PathID(r, "id")
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	p, err := h.App.Store.GetProject(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return nil, false
	}
	return &p, true
}

func redirect(w http.ResponseWriter, r *http.Request, path, msg string) {
	if msg != "" {
		path += "?msg=" + msg
	}
	http.Redirect(w, r, path, http.StatusSeeOther)
}

// --- Вход и регистрация ---

// authView — страница входа/регистрации с переключателем режимов.
type authView struct {
	Base
	Mode  string // login | register
	Next  string
	Login string
	Cells []DecorCell // декоративная «тепловая карта» на левой панели
	Rules struct{ MinLogin, MaxLogin, MinPassword, MaxPassword int }
}

func (h *Handler) renderAuth(w http.ResponseWriter, r *http.Request, status int, mode, login, errMsg string) {
	title := "Вход"
	if mode == "register" {
		title = "Регистрация"
	}
	v := authView{Base: Base{Title: title, Accent: accentOf(r), Error: errMsg, Flash: flashes[r.URL.Query().Get("msg")]}, Mode: mode,
		Next: httpx.SafeNext(r.FormValue("next")), Login: login, Cells: decorCells()}
	v.Rules.MinLogin, v.Rules.MaxLogin = httpx.MinLoginLen, httpx.MaxLoginLen
	v.Rules.MinPassword, v.Rules.MaxPassword = httpx.MinPasswordLen, httpx.MaxPasswordLen
	h.render(w, r, status, "login.html", v)
}

// DecorCell — клетка декоративного узора: уровень 0…5, колонка и строка (для волны подсветки).
type DecorCell struct{ Level, Col, Row int }

// decorCells — узор 30×10 уровней 0…5 для левой панели страницы входа.
func decorCells() []DecorCell {
	cells := make([]DecorCell, 0, 300)
	for row := range 10 {
		for c := range 30 {
			r, cf := float64(row), float64(c)
			wave := math.Sin(cf/3.1+r/2.2) + math.Sin(cf/1.7-r/3.3)*0.6 + cf/30*1.2
			bump := 0.0
			if (row*7+c*3)%5 == 0 {
				bump = 1
			}
			cells = append(cells, DecorCell{Level: max(0, min(5, int(math.Round(wave*1.6+1.4+bump)))), Col: c, Row: row})
		}
	}
	return cells
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	if h.Auth.LoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.renderAuth(w, r, http.StatusOK, "login", r.URL.Query().Get("login"), "")
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSpace(r.PostFormValue("login"))
	_, err := h.Auth.Login(w, r, login, r.PostFormValue("password"))
	switch {
	case err == nil:
		http.Redirect(w, r, httpx.SafeNext(r.PostFormValue("next")), http.StatusSeeOther)
	case errors.Is(err, httpx.ErrTooManyAttempts):
		h.renderAuth(w, r, http.StatusTooManyRequests, "login", login, "Слишком много попыток. Повторите через 15 минут.")
	case errors.Is(err, httpx.ErrBadCredentials):
		h.renderAuth(w, r, http.StatusUnauthorized, "login", login, "Неверный логин или пароль.")
	default:
		h.App.Log.Error("вход", "err", err)
		h.renderAuth(w, r, http.StatusInternalServerError, "login", login, "Внутренняя ошибка сервера. Повторите позже.")
	}
}

func (h *Handler) registerPage(w http.ResponseWriter, r *http.Request) {
	if h.Auth.LoggedIn(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.renderAuth(w, r, http.StatusOK, "register", "", "")
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	login := strings.TrimSpace(r.PostFormValue("login"))
	u, err := h.Auth.Register(r, login, r.PostFormValue("password"), r.PostFormValue("password_confirm"))
	if err != nil {
		status, msg := httpx.StatusOf(err)
		if errors.Is(err, httpx.ErrTooManyAttempts) {
			status, msg = http.StatusTooManyRequests, "Слишком много попыток. Повторите через 15 минут."
		}
		if status >= 500 {
			h.App.Log.Error("регистрация", "err", err)
		}
		h.renderAuth(w, r, status, "register", login, msg)
		return
	}
	h.App.Log.Info("зарегистрирован пользователь", "user", u.ID, "login", u.Login)
	http.Redirect(w, r, "/login?msg=registered&login="+url.QueryEscape(u.Login), http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	h.Auth.Logout(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// --- Проекты ---

type indexView struct {
	Base
	Projects []domain.Project
	Running  map[int64]bool
	Form     app.ProjectInput
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	h.renderIndex(w, r, http.StatusOK, app.ProjectInput{}, "")
}

func (h *Handler) renderIndex(w http.ResponseWriter, r *http.Request, status int, form app.ProjectInput, errMsg string) {
	ps, err := h.App.Store.ListUserProjects(r.Context(), httpx.User(r).ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := indexView{Base: h.base(r, "Проекты"), Projects: ps, Running: map[int64]bool{}, Form: form}
	v.Error = errMsg
	for _, p := range ps {
		if h.App.IsRunning(p.ID) {
			v.Running[p.ID] = true
			v.Refresh = true
		}
	}
	h.render(w, r, status, "index.html", v)
}

func formInput(r *http.Request) app.ProjectInput {
	return app.ProjectInput{
		Name: r.PostFormValue("name"), RepoURL: r.PostFormValue("repo_url"), Branch: r.PostFormValue("branch"),
		Provider: r.PostFormValue("provider"), ProviderURL: r.PostFormValue("provider_url"),
		ProviderProjectID: r.PostFormValue("provider_project_id"), Token: r.PostFormValue("token"),
		ClearToken: r.PostFormValue("clear_token") == "on",
	}
}

func (h *Handler) createProject(w http.ResponseWriter, r *http.Request) {
	in := formInput(r)
	p, err := h.App.CreateProject(r.Context(), httpx.User(r).ID, in)
	if err != nil {
		in.Token = "" // токен не возвращаем в форму
		status, msg := httpx.StatusOf(err)
		h.renderIndex(w, r, status, in, msg)
		return
	}
	_ = h.App.SyncAsync(p.ID)
	redirect(w, r, fmt.Sprintf("/projects/%d", p.ID), "created")
}

func (h *Handler) sync(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	if err := h.App.SyncAsync(p.ID); err != nil && !errors.Is(err, domain.ErrSyncActive) {
		h.fail(w, r, err)
		return
	}
	back := fmt.Sprintf("/projects/%d", p.ID)
	if b := r.PostFormValue("back"); b != "" {
		back = httpx.SafeNext(b)
	}
	redirect(w, r, back, "sync")
}

// --- Дашборд ---

// Filter — выбранные на странице период и команда.
type Filter struct {
	From, To string
	TeamID   int64
	Preset   int
	Presets  []int
}

func parseFilter(r *http.Request) (Filter, domain.Period, error) {
	q := r.URL.Query()
	f := Filter{Presets: []int{7, 30, 90, 180, 365}}
	now := time.Now()
	var p domain.Period
	var err error
	if days, _ := strconv.Atoi(q.Get("days")); days > 0 && days <= 1830 {
		f.Preset = days
		p = app.DefaultPeriod(days, now)
	} else if q.Get("from") != "" || q.Get("to") != "" {
		p, err = app.ParsePeriod(q.Get("from"), q.Get("to"), now)
	} else {
		f.Preset = 30
		p = app.DefaultPeriod(30, now)
	}
	f.From = p.From.Format("2006-01-02")
	f.To = p.To.Add(-time.Second).Format("2006-01-02")
	f.TeamID, _ = strconv.ParseInt(q.Get("team"), 10, 64)
	return f, p, err
}

// KPI — плитка ключевой метрики.
type KPI struct {
	Title, Value, Unit, Delta, Verdict, VerdictText, Description string
}

// HeatRow — строка тепловой карты.
type HeatRow struct {
	Day   string
	Cells []HeatCell
}

// HeatCell — ячейка тепловой карты.
type HeatCell struct {
	Count, Level int
	Title        string
}

// ChartData — данные графиков (вставляются в страницу как JSON).
type ChartData struct {
	Weeks    []string   `json:"weeks"`
	Commits  []int      `json:"commits"`
	Lead     []*float64 `json:"lead"`
	MRMerged []int      `json:"mr_merged"`
}

// MetricRow — строка полной таблицы метрик.
type MetricRow struct {
	analysis.Trend
	Description string
	Group       string
}

type dashboardView struct {
	Base
	Filter       Filter
	Teams        []domain.Team
	Data         *report.Data
	KPIs         []KPI
	Metrics      []MetricRow
	Heatmap      []HeatRow
	Charts       ChartData
	Points       map[int64]int
	Events       []zerocracy.Event
	AuthorNames  map[int64]string
	Running      bool
	AuthorView   bool
	AuthorID     int64
	TotalCommits int
	WorkStart    int // рамка рабочего дня на тепловой карте
	WorkEnd      int
}

func (h *Handler) dashboard(w http.ResponseWriter, r *http.Request) {
	h.analysisPage(w, r, 0)
}

func (h *Handler) author(w http.ResponseWriter, r *http.Request) {
	aid, err := api.PathID(r, "aid")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.analysisPage(w, r, aid)
}

func (h *Handler) analysisPage(w http.ResponseWriter, r *http.Request, authorID int64) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	f, period, err := parseFilter(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	scope := app.Scope{TeamID: f.TeamID, AuthorID: authorID}
	if authorID != 0 {
		scope.TeamID, f.TeamID = 0, 0
	}
	d, err := h.App.Analyze(r.Context(), p.ID, period, scope)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	teams, err := h.App.Store.ListTeams(r.Context(), p.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := dashboardView{
		Base: h.base(r, p.Name), Filter: f, Teams: teams, Data: d, Running: h.App.IsRunning(p.ID),
		AuthorView: authorID != 0, AuthorID: authorID, Points: map[int64]int{}, AuthorNames: map[int64]string{},
		WorkStart: h.App.Cfg.WorkdayStart, WorkEnd: h.App.Cfg.WorkdayEnd,
	}
	v.Project, v.Tab, v.Refresh = p, "dashboard", v.Running
	if authorID != 0 {
		v.Title = strings.TrimPrefix(d.Scope, "Разработчик: ") + " — " + p.Name
	}
	v.KPIs = kpis(d, authorID != 0)
	v.Metrics = metricRows(d.Trends)
	v.Heatmap = heatmap(d.Snapshot.Heatmap)
	for _, s := range d.Contribution.Scores {
		v.Points[s.AuthorID] = s.Points
		v.AuthorNames[s.AuthorID] = s.Name
	}
	for _, a := range d.Snapshot.Authors {
		v.AuthorNames[a.AuthorID] = a.Name
	}
	v.Events = d.Contribution.Events
	if len(v.Events) > 50 {
		v.Events = v.Events[:50]
	}
	v.Charts = charts(d)
	h.render(w, r, http.StatusOK, "dashboard.html", v)
}

func kpis(d *report.Data, author bool) []KPI {
	keys := []string{"commits", "active_days", "median_commit_size", "test_ratio", "message_quality", "rework_rate", "off_hours_share", "bus_factor"}
	if d.Snapshot.HasProviderData {
		keys = []string{"commits", "mr_merged", "mr_lead_time_median_h", "first_review_median_h", "review_coverage", "test_ratio", "rework_rate", "bus_factor"}
	}
	if author {
		keys[len(keys)-1] = "reviews_given"
	}
	byKey := map[string]analysis.Trend{}
	for _, t := range d.Trends {
		byKey[t.Key] = t
	}
	out := make([]KPI, 0, len(keys))
	for _, k := range keys {
		t := byKey[k]
		def, _ := metrics.DefinitionByKey(k)
		value, unit := splitUnit(formatValue(t.Unit, t.Current))
		out = append(out, KPI{
			Title: def.Title, Value: value, Unit: unit, Delta: report.Delta(t.DeltaPct),
			Verdict: string(t.Verdict), VerdictText: verdictShort(t.Verdict), Description: def.Description,
		})
	}
	return out
}

func metricRows(trends []analysis.Trend) []MetricRow {
	rows := make([]MetricRow, 0, len(trends))
	for _, t := range trends {
		def, _ := metrics.DefinitionByKey(t.Key)
		rows = append(rows, MetricRow{Trend: t, Description: def.Description, Group: def.Group})
	}
	return rows
}

var weekdays = []string{"Пн", "Вт", "Ср", "Чт", "Пт", "Сб", "Вс"}

// splitUnit отделяет единицу измерения от числа: «18.4 ч» → «18.4», «ч»; «87%» → «87», «%».
func splitUnit(s string) (string, string) {
	if v, ok := strings.CutSuffix(s, "%"); ok {
		return v, "%"
	}
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// heatmap переводит счётчики в 6 уровней последовательной шкалы (CSS-классы, без inline-стилей).
func heatmap(m [7][24]int) []HeatRow {
	maxV := 0
	for _, row := range m {
		for _, v := range row {
			maxV = max(maxV, v)
		}
	}
	rows := make([]HeatRow, 7)
	for d := range 7 {
		rows[d].Day = weekdays[d]
		for hr := range 24 {
			c := m[d][hr]
			level := 0
			if c > 0 && maxV > 0 {
				level = max(1, int(math.Ceil(float64(c)/float64(maxV)*5)))
			}
			rows[d].Cells = append(rows[d].Cells, HeatCell{Count: c, Level: level,
				Title: fmt.Sprintf("%s %02d:00 — коммитов: %d", weekdays[d], hr, c)})
		}
	}
	return rows
}

func charts(d *report.Data) ChartData {
	var c ChartData
	for _, w := range d.Snapshot.Weekly {
		c.Weeks = append(c.Weeks, w.WeekStart.Format("02.01"))
		c.Commits = append(c.Commits, w.Commits)
		c.Lead = append(c.Lead, w.MRLeadMedianH)
		c.MRMerged = append(c.MRMerged, w.MRMerged)
	}
	return c
}

// --- Отчёты ---

type reportsView struct {
	Base
	Filter  Filter
	Teams   []domain.Team
	Reports []domain.StoredReport
	TeamMap map[int64]string
}

func (h *Handler) reports(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	f, _, _ := parseFilter(r)
	rs, err := h.App.Store.ListReports(r.Context(), p.ID, 100)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	teams, _ := h.App.Store.ListTeams(r.Context(), p.ID)
	v := reportsView{Base: h.base(r, "Отчёты — "+p.Name), Filter: f, Teams: teams, Reports: rs, TeamMap: map[int64]string{}}
	for _, t := range teams {
		v.TeamMap[t.ID] = t.Name
	}
	v.Project, v.Tab = p, "reports"
	h.render(w, r, http.StatusOK, "reports.html", v)
}

func (h *Handler) createReport(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	period, err := app.ParsePeriod(r.PostFormValue("from"), r.PostFormValue("to"), time.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	teamID, _ := strconv.ParseInt(r.PostFormValue("team"), 10, 64)
	if _, err := h.App.GenerateReport(r.Context(), p.ID, period, teamID, "manual"); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/projects/%d/reports", p.ID), "report")
}

func (h *Handler) report(w http.ResponseWriter, r *http.Request) {
	rid, err := api.PathID(r, "rid")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "html"
	}
	if err := api.ServeReport(w, r, h.App, rid, format); err != nil {
		h.fail(w, r, err)
	}
}

// --- Настройки ---

type settingsView struct {
	Base
	Form    app.ProjectInput
	Authors []domain.Author
	Teams   []domain.Team
	Members map[int64]map[int64]bool
}

func (h *Handler) settings(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	h.renderSettings(w, r, p, http.StatusOK, app.ProjectInput{
		Name: p.Name, RepoURL: p.RepoURL, Branch: p.Branch, Provider: p.Provider,
		ProviderURL: p.ProviderURL, ProviderProjectID: p.ProviderProjectID,
	}, "")
}

func (h *Handler) renderSettings(w http.ResponseWriter, r *http.Request, p *domain.Project, status int, form app.ProjectInput, errMsg string) {
	authors, err := h.App.Store.ListAuthors(r.Context(), p.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	teams, err := h.App.Store.ListTeams(r.Context(), p.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := settingsView{Base: h.base(r, "Настройки — "+p.Name), Form: form, Authors: authors, Teams: teams,
		Members: map[int64]map[int64]bool{}}
	for _, t := range teams {
		v.Members[t.ID] = map[int64]bool{}
		for _, m := range t.MemberIDs {
			v.Members[t.ID][m] = true
		}
	}
	v.Project, v.Tab, v.Error = p, "settings", errMsg
	h.render(w, r, status, "settings.html", v)
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	in := formInput(r)
	if _, err := h.App.UpdateProject(r.Context(), p.ID, in); err != nil {
		in.Token = ""
		status, msg := httpx.StatusOf(err)
		h.renderSettings(w, r, p, status, in, msg)
		return
	}
	redirect(w, r, fmt.Sprintf("/projects/%d/settings", p.ID), "saved")
}

func (h *Handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	if r.PostFormValue("confirm") != p.Name {
		h.renderSettings(w, r, p, http.StatusBadRequest, app.ProjectInput{Name: p.Name, RepoURL: p.RepoURL, Branch: p.Branch,
			Provider: p.Provider, ProviderURL: p.ProviderURL, ProviderProjectID: p.ProviderProjectID},
			"Для удаления введите точное название проекта.")
		return
	}
	if err := h.App.DeleteProject(r.Context(), p.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, "/", "deleted")
}

func (h *Handler) saveTeam(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	t := domain.Team{ProjectID: p.ID, Name: r.PostFormValue("name")}
	t.ID, _ = strconv.ParseInt(r.PostFormValue("team_id"), 10, 64)
	for _, v := range r.PostForm["members"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			t.MemberIDs = append(t.MemberIDs, id)
		}
	}
	if err := h.App.Store.SaveTeam(r.Context(), &t); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/projects/%d/settings", p.ID), "team")
}

func (h *Handler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	tid, err := api.PathID(r, "tid")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.App.Store.DeleteTeam(r.Context(), p.ID, tid); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/projects/%d/settings", p.ID), "team_deleted")
}

func (h *Handler) mergeAuthors(w http.ResponseWriter, r *http.Request) {
	p, ok := h.project(w, r)
	if !ok {
		return
	}
	dst, _ := strconv.ParseInt(r.PostFormValue("target"), 10, 64)
	src, _ := strconv.ParseInt(r.PostFormValue("source"), 10, 64)
	if err := h.App.Store.MergeAuthors(r.Context(), p.ID, dst, src); err != nil {
		h.fail(w, r, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/projects/%d/settings", p.ID), "merged")
}

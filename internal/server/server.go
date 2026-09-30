// Package server собирает HTTP-обработчики API и веб-интерфейса в один handler.
package server

import (
	"net/http"

	"teampulse/internal/api"
	"teampulse/internal/app"
	"teampulse/internal/httpx"
	"teampulse/internal/web"
)

// NewAuth создаёт аутентификацию из конфигурации приложения.
func NewAuth(a *app.App) *httpx.Auth {
	return &httpx.Auth{
		Box: a.Box, Users: a.Store, APIToken: a.Cfg.APIToken, SecureCookie: a.Cfg.CookieSecure,
	}
}

// New возвращает корневой handler со всеми маршрутами и middleware.
func New(a *app.App, auth *httpx.Auth) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.Ping(r.Context()); err != nil {
			httpx.JSONError(w, http.StatusServiceUnavailable, "база данных недоступна")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	(&api.Handler{App: a, Auth: auth}).Register(mux)
	pages, err := web.New(a, auth)
	if err != nil {
		return nil, err
	}
	pages.Register(mux)
	return httpx.Chain(mux, httpx.Logging(a.Log), httpx.SecurityHeaders, httpx.LimitBody(1<<20)), nil
}

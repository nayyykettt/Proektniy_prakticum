package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"teampulse/internal/domain"
)

// Chain применяет middleware в порядке перечисления (первый — внешний).
func Chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// ContentSecurityPolicy — строгая политика: только собственные скрипты и стили.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// SecurityHeaders выставляет заголовки безопасности.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// LimitBody ограничивает размер тела запроса.
func LimitBody(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Logging пишет access-лог и перехватывает паники (сервис не падает из-за одного запроса).
func Logging(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic", "err", rec, "path", r.URL.Path, "stack", string(debug.Stack()))
					http.Error(sw, "Внутренняя ошибка сервера", http.StatusInternalServerError)
				}
				if r.URL.Path != "/healthz" {
					log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
						"duration", time.Since(start).Round(time.Microsecond))
				}
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

// StatusOf сопоставляет ошибку прикладного слоя HTTP-статусу и безопасному тексту.
func StatusOf(err error) (int, string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, domain.ErrInvalid):
		return http.StatusBadRequest, err.Error()
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrSyncActive):
		return http.StatusConflict, err.Error()
	}
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return http.StatusRequestEntityTooLarge, "слишком большой запрос"
	}
	return http.StatusInternalServerError, "внутренняя ошибка сервера"
}

// JSON пишет ответ в формате JSON.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// JSONError пишет ошибку {"error": "..."}.
func JSONError(w http.ResponseWriter, status int, msg string) {
	JSON(w, status, map[string]string{"error": msg})
}

// Fail пишет ошибку прикладного слоя; внутренние детали уходят только в лог.
func Fail(w http.ResponseWriter, log *slog.Logger, err error) {
	status, msg := StatusOf(err)
	if status >= 500 {
		log.Error("ошибка обработки запроса", "err", err)
	}
	JSONError(w, status, msg)
}

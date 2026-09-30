// Package httpx — общие для API и веб-интерфейса middleware: авторизация,
// CSRF, заголовки безопасности, логирование, обработка ошибок.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"teampulse/internal/domain"
	"teampulse/internal/secure"
)

const (
	cookieName = "tp_session"
	sessionTTL = 12 * time.Hour

	// Ограничения учётных данных.
	MinLoginLen    = 3
	MaxLoginLen    = 32
	MinPasswordLen = 8
	MaxPasswordLen = 128
)

type ctxKey struct{}

type identity struct {
	user    domain.User
	session string
	bearer  bool // сервисный API-токен: доступ ко всем проектам
}

func (id identity) ok() bool { return id.bearer || id.user.ID != 0 }

// Users — хранилище учётных записей.
type Users interface {
	CreateUser(ctx context.Context, login, passwordHash string) (domain.User, error)
	GetUser(ctx context.Context, id int64) (domain.User, error)
	UserByLogin(ctx context.Context, login string) (domain.User, string, error)
}

// Auth — аутентификация пользователей (логин/пароль, cookie-сессия) и Bearer-токен для API.
type Auth struct {
	Box          *secure.Box
	Users        Users
	APIToken     string
	SecureCookie bool

	mu       sync.Mutex
	attempts map[string]*attempt
}

type attempt struct {
	count int
	since time.Time
}

func (a *Auth) identify(r *http.Request) identity {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") && a.APIToken != "" {
		if secure.Equal(strings.TrimPrefix(h, "Bearer "), a.APIToken) {
			return identity{bearer: true}
		}
		return identity{}
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return identity{}
	}
	subject, ok := a.Box.ParseSession(c.Value)
	if !ok {
		return identity{}
	}
	uid, err := strconv.ParseInt(subject, 10, 64)
	if err != nil || uid <= 0 {
		return identity{}
	}
	u, err := a.Users.GetUser(r.Context(), uid)
	if err != nil { // пользователь удалён или БД недоступна — сессия недействительна
		return identity{}
	}
	return identity{user: u, session: c.Value}
}

func current(r *http.Request) identity {
	id, _ := r.Context().Value(ctxKey{}).(identity)
	return id
}

// User возвращает текущего пользователя (ID == 0 — не вошёл или сервисный токен).
func User(r *http.Request) domain.User { return current(r).user }

// IsService — запрос по сервисному API-токену.
func IsService(r *http.Request) bool { return current(r).bearer }

// CanAccess — может ли текущий запрос видеть и изменять проект.
// Пользователь — только свои проекты; сервисный API-токен — все.
func CanAccess(r *http.Request, p domain.Project) bool {
	id := current(r)
	return id.bearer || (id.user.ID != 0 && p.OwnerID == id.user.ID)
}

// CSRFToken — токен для скрытого поля форм текущего пользователя.
func (a *Auth) CSRFToken(r *http.Request) string {
	return a.Box.CSRFToken(current(r).session)
}

// Web требует входа для страниц: неаутентифицированных отправляет на /login.
// Для POST-форм проверяет CSRF-токен.
func (a *Auth) Web(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := a.identify(r)
		if !id.ok() {
			http.Redirect(w, r, "/login?next="+safeNext(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && !a.Box.CheckCSRF(id.session, r.PostFormValue("csrf")) {
			http.Error(w, "Недействительный CSRF-токен. Обновите страницу и повторите.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// API требует Bearer-токен или сессию. Изменяющие запросы по cookie требуют заголовок X-CSRF-Token.
func (a *Auth) API(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := a.identify(r)
		if !id.ok() {
			JSONError(w, http.StatusUnauthorized, "требуется авторизация: Bearer-токен (API_TOKEN) или вход в систему")
			return
		}
		unsafe := r.Method != http.MethodGet && r.Method != http.MethodHead
		if unsafe && !id.bearer && !a.Box.CheckCSRF(id.session, r.Header.Get("X-CSRF-Token")) {
			JSONError(w, http.StatusForbidden, "отсутствует или неверен X-CSRF-Token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}

// ErrTooManyAttempts — превышен лимит попыток входа/регистрации.
var ErrTooManyAttempts = errors.New("слишком много попыток — повторите через 15 минут")

// ErrBadCredentials — неверный логин или пароль (не уточняем, что именно).
var ErrBadCredentials = errors.New("неверный логин или пароль")

// ValidateCredentials проверяет логин и пароль с повтором при регистрации.
func ValidateCredentials(login, password, confirm string) error {
	if n := utf8.RuneCountInString(login); n < MinLoginLen || n > MaxLoginLen {
		return fmt.Errorf("%w: логин — от %d до %d символов", domain.ErrInvalid, MinLoginLen, MaxLoginLen)
	}
	for _, c := range login {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return fmt.Errorf("%w: логин может содержать только латинские буквы, цифры и символы . _ -", domain.ErrInvalid)
		}
	}
	if n := utf8.RuneCountInString(password); n < MinPasswordLen || n > MaxPasswordLen {
		return fmt.Errorf("%w: пароль — от %d до %d символов", domain.ErrInvalid, MinPasswordLen, MaxPasswordLen)
	}
	if strings.EqualFold(password, login) {
		return fmt.Errorf("%w: пароль не должен совпадать с логином", domain.ErrInvalid)
	}
	if password != confirm {
		return fmt.Errorf("%w: пароли не совпадают", domain.ErrInvalid)
	}
	return nil
}

// Register создаёт учётную запись. Не выполняет вход: пользователь входит отдельно.
func (a *Auth) Register(r *http.Request, login, password, confirm string) (domain.User, error) {
	login = strings.TrimSpace(login)
	if !a.allow("reg:" + clientIP(r)) { // отдельный от входа счётчик
		return domain.User{}, ErrTooManyAttempts
	}
	if err := ValidateCredentials(login, password, confirm); err != nil {
		return domain.User{}, err
	}
	hash, err := secure.HashPassword(password)
	if err != nil {
		return domain.User{}, err
	}
	return a.Users.CreateUser(r.Context(), login, hash)
}

// Login проверяет логин и пароль и выставляет cookie сессии.
// Ограничивает перебор: не более 10 попыток за 15 минут с одного IP.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, login, password string) (domain.User, error) {
	ip := clientIP(r)
	if !a.allow(ip) {
		return domain.User{}, ErrTooManyAttempts
	}
	u, hash, err := a.Users.UserByLogin(r.Context(), strings.TrimSpace(login))
	if errors.Is(err, domain.ErrNotFound) {
		secure.CheckPassword(password, secure.DummyHash()) // выравниваем время ответа
		return domain.User{}, ErrBadCredentials
	}
	if err != nil {
		return domain.User{}, err
	}
	if !secure.CheckPassword(password, hash) {
		return domain.User{}, ErrBadCredentials
	}
	a.reset(ip)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: a.Box.NewSession(strconv.FormatInt(u.ID, 10), sessionTTL), Path: "/",
		HttpOnly: true, Secure: a.SecureCookie, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds()),
	})
	return u, nil
}

// LoggedIn — есть ли у запроса действующая пользовательская сессия (для страниц входа).
func (a *Auth) LoggedIn(r *http.Request) bool { return a.identify(r).user.ID != 0 }

// Logout удаляет сессию.
func (a *Auth) Logout(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: a.SecureCookie, SameSite: http.SameSiteStrictMode})
}

func (a *Auth) allow(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.attempts == nil {
		a.attempts = map[string]*attempt{}
	}
	at := a.attempts[ip]
	if at == nil || time.Since(at.since) > 15*time.Minute {
		at = &attempt{since: time.Now()}
		a.attempts[ip] = at
	}
	at.count++
	return at.count <= 10
}

func (a *Auth) reset(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.attempts, ip)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// safeNext допускает только локальные пути для редиректа после входа (защита от open redirect).
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	return next
}

// SafeNext — экспортируемая версия для обработчика входа.
func SafeNext(next string) string { return safeNext(next) }

// Package config загружает настройки сервиса из переменных окружения.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config — все настройки сервиса. Источник — только переменные окружения,
// чтобы одинаково работать локально, в Docker и на любом хостинге.
type Config struct {
	Addr     string // адрес HTTP-сервера
	DataDir  string // каталог для БД и клонов репозиториев
	DBPath   string
	ReposDir string

	AppSecret    []byte // ключ для шифрования токенов и подписи cookie
	APIToken     string // сервисный Bearer-токен для JSON API (доступ ко всем проектам)
	CookieSecure bool

	AllowLocalRepos bool          // разрешить локальные пути и приватные адреса (dev/тесты)
	MaxCommits      int           // максимум новых коммитов за одну синхронизацию
	CloneTimeout    time.Duration // таймаут клонирования/fetch
	SyncInterval    time.Duration // период автосинхронизации (0 — выключено)
	ReportInterval  time.Duration // период автогенерации отчётов (0 — выключено)
	ExcludePatterns []string      // дополнительные шаблоны исключаемых файлов

	WorkdayStart int // начало рабочего дня (час, локальное время коммита)
	WorkdayEnd   int // конец рабочего дня

	PolicyPath string // путь к политике Zerocracy (JSON); пусто — политика по умолчанию
	AutoAssign bool   // назначать задачи в Т1 Код автоматически
}

// Load читает окружение и валидирует значения.
func Load() (*Config, error) {
	c := &Config{
		Addr:            env("ADDR", ":8080"),
		DataDir:         env("DATA_DIR", "./data"),
		APIToken:        os.Getenv("API_TOKEN"),
		PolicyPath:      os.Getenv("ZEROCRACY_POLICY"),
		ExcludePatterns: splitList(os.Getenv("EXCLUDE_PATTERNS")),
	}
	var errs []error
	c.CookieSecure = envBool("COOKIE_SECURE", false, &errs)
	c.AllowLocalRepos = envBool("ALLOW_LOCAL_REPOS", false, &errs)
	c.AutoAssign = envBool("ZEROCRACY_AUTO_ASSIGN", false, &errs)
	c.MaxCommits = envInt("MAX_COMMITS", 20000, &errs)
	c.WorkdayStart = envInt("WORKDAY_START", 9, &errs)
	c.WorkdayEnd = envInt("WORKDAY_END", 19, &errs)
	c.CloneTimeout = envDuration("CLONE_TIMEOUT", 10*time.Minute, &errs)
	c.SyncInterval = envDuration("SYNC_INTERVAL", 6*time.Hour, &errs)
	c.ReportInterval = envDuration("REPORT_INTERVAL", 7*24*time.Hour, &errs)

	if c.MaxCommits <= 0 {
		errs = append(errs, errors.New("MAX_COMMITS должен быть > 0"))
	}
	if c.WorkdayStart < 0 || c.WorkdayEnd > 24 || c.WorkdayStart >= c.WorkdayEnd {
		errs = append(errs, errors.New("WORKDAY_START/WORKDAY_END: ожидается 0 <= start < end <= 24"))
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	c.DBPath = env("DB_PATH", filepath.Join(c.DataDir, "teampulse.db"))
	c.ReposDir = filepath.Join(c.DataDir, "repos")
	if err := os.MkdirAll(c.ReposDir, 0o750); err != nil {
		return nil, fmt.Errorf("создание каталога данных: %w", err)
	}
	secret, err := loadSecret(c.DataDir)
	if err != nil {
		return nil, err
	}
	c.AppSecret = secret
	return c, nil
}

// loadSecret берёт APP_SECRET из окружения, иначе — из файла в DATA_DIR
// (создаёт его при первом запуске), чтобы зашифрованные токены переживали рестарт.
func loadSecret(dataDir string) ([]byte, error) {
	if s := os.Getenv("APP_SECRET"); s != "" {
		if len(s) < 16 {
			return nil, errors.New("APP_SECRET должен быть не короче 16 символов")
		}
		return []byte(s), nil
	}
	path := filepath.Join(dataDir, "secret.key")
	if b, err := os.ReadFile(path); err == nil {
		return hexDecode(strings.TrimSpace(string(b)))
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(buf)), 0o600); err != nil {
		return nil, fmt.Errorf("сохранение секрета: %w", err)
	}
	return buf, nil
}

func hexDecode(s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) < 16 {
		return nil, errors.New("повреждён файл secret.key")
	}
	return b, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool, errs *[]error) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: ожидается true/false", key))
	}
	return b
}

func envInt(key string, def int, errs *[]error) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s: ожидается целое число", key))
	}
	return n
}

func envDuration(key string, def time.Duration, errs *[]error) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		*errs = append(*errs, fmt.Errorf("%s: ожидается длительность, например 6h", key))
	}
	return d
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

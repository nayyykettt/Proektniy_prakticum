package gitparse

import (
	"path"
	"strings"
)

// DefaultExclude — файлы, которые не отражают работу разработчика:
// зависимости, lock-файлы, сборка, сгенерированный и минифицированный код.
var DefaultExclude = []string{
	"vendor/", "node_modules/", "third_party/", "dist/", "build/", ".idea/", ".vscode/",
	"go.sum", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "Cargo.lock", "poetry.lock",
	"composer.lock", "Gemfile.lock", "*.lock",
	"*.min.js", "*.min.css", "*.map", "*.pb.go", "*_pb2.py", "*.gen.go", "*_generated.*", "*.generated.*",
	"*.svg", "*.png", "*.jpg", "*.jpeg", "*.gif", "*.ico", "*.pdf", "*.zip",
}

// Classifier определяет тип файла по пути.
type Classifier struct {
	dirs  []string // шаблоны каталогов ("vendor/")
	globs []string // шаблоны имён файлов ("*.lock", "go.sum")
}

// NewClassifier объединяет шаблоны по умолчанию с пользовательскими.
func NewClassifier(extra []string) *Classifier {
	c := &Classifier{}
	for _, p := range append(append([]string{}, DefaultExclude...), extra...) {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case strings.HasSuffix(p, "/"):
			c.dirs = append(c.dirs, strings.Trim(p, "/"))
		default:
			c.globs = append(c.globs, p)
		}
	}
	return c
}

// Excluded сообщает, исключается ли файл из метрик.
func (c *Classifier) Excluded(p string) bool {
	p = strings.TrimPrefix(p, "/")
	segments := strings.Split(p, "/")
	for _, dir := range c.dirs {
		for _, s := range segments[:len(segments)-1] {
			if s == dir {
				return true
			}
		}
		if strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	base := path.Base(p)
	for _, g := range c.globs {
		if ok, _ := path.Match(g, base); ok {
			return true
		}
	}
	return false
}

// IsTest распознаёт тестовые файлы популярных языков и фреймворков.
func IsTest(p string) bool {
	lp := strings.ToLower(p)
	base := path.Base(lp)
	for _, seg := range strings.Split(path.Dir(lp), "/") {
		switch seg {
		case "test", "tests", "__tests__", "spec", "specs", "testdata", "e2e":
			return true
		}
	}
	return strings.Contains(base, "_test.") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.HasPrefix(base, "test_") ||
		strings.HasSuffix(strings.TrimSuffix(base, path.Ext(base)), "test") && path.Ext(base) == ".java"
}

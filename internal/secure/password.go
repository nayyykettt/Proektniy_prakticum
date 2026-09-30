package secure

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Параметры PBKDF2-HMAC-SHA256 (рекомендация OWASP 2023+).
const (
	pbkdf2Iter = 600_000
	saltLen    = 16
	keyLen     = 32
)

// HashPassword возвращает хеш пароля "pbkdf2-sha256$<итерации>$<соль>$<ключ>" (base64url без паддинга).
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iter, keyLen)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iter, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// CheckPassword сравнивает пароль с хешем за постоянное время.
func CheckPassword(password, hash string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	enc := base64.RawURLEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// DummyHash — хеш для сравнения, когда пользователь не найден: время ответа
// не выдаёт, существует ли логин.
var DummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("teampulse-dummy-password")
	return h
})

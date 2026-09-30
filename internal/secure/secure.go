// Package secure — шифрование секретов, подпись сессий и проверка внешних URL.
package secure

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Box шифрует и расшифровывает токены доступа (AES-256-GCM).
type Box struct {
	aead cipher.AEAD
	mac  []byte
}

// NewBox выводит из секрета приложения независимые ключи для шифрования и подписи.
func NewBox(secret []byte) (*Box, error) {
	encKey := derive(secret, "token-encryption")
	block, err := aes.NewCipher(encKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, mac: derive(secret, "session-signing")}, nil
}

func derive(secret []byte, purpose string) []byte {
	h := hmac.New(sha256.New, secret)
	h.Write([]byte(purpose))
	return h.Sum(nil)
}

// Encrypt шифрует открытый текст; пустая строка остаётся пустой.
func (b *Box) Encrypt(plain string) ([]byte, error) {
	if plain == "" {
		return nil, nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

// Decrypt расшифровывает данные, зашифрованные Encrypt.
func (b *Box) Decrypt(data []byte) (string, error) {
	if len(data) == 0 {
		return "", nil
	}
	ns := b.aead.NonceSize()
	if len(data) < ns {
		return "", errors.New("повреждённый шифротекст")
	}
	plain, err := b.aead.Open(nil, data[:ns], data[ns:], nil)
	if err != nil {
		return "", errors.New("не удалось расшифровать токен (сменился APP_SECRET?)")
	}
	return string(plain), nil
}

// Sign подписывает значение: "<value>.<hmac>".
func (b *Box) Sign(value string) string {
	return value + "." + b.sig(value)
}

// Verify проверяет подпись и возвращает исходное значение.
func (b *Box) Verify(signed string) (string, bool) {
	i := strings.LastIndexByte(signed, '.')
	if i < 0 {
		return "", false
	}
	value, sig := signed[:i], signed[i+1:]
	if !hmac.Equal([]byte(sig), []byte(b.sig(value))) {
		return "", false
	}
	return value, true
}

func (b *Box) sig(value string) string {
	h := hmac.New(sha256.New, b.mac)
	h.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// NewSession создаёт подписанное значение cookie: "<субъект>|<истекает>|<nonce>".
// Субъект — ID пользователя.
func (b *Box) NewSession(subject string, ttl time.Duration) string {
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce)
	exp := time.Now().Add(ttl).Unix()
	return b.Sign(fmt.Sprintf("%s|%d|%s", subject, exp, base64.RawURLEncoding.EncodeToString(nonce)))
}

// ParseSession проверяет cookie и возвращает субъект сессии.
func (b *Box) ParseSession(cookie string) (string, bool) {
	v, ok := b.Verify(cookie)
	if !ok {
		return "", false
	}
	parts := strings.Split(v, "|")
	if len(parts) != 3 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[0], true
}

// CSRFToken — токен формы, привязанный к сессии.
func (b *Box) CSRFToken(session string) string {
	return b.sig("csrf:" + session)
}

// CheckCSRF сравнивает токен формы с ожидаемым за постоянное время.
func (b *Box) CheckCSRF(session, token string) bool {
	return subtle.ConstantTimeCompare([]byte(b.CSRFToken(session)), []byte(token)) == 1
}

// Equal сравнивает секреты за постоянное время.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Mask скрывает секрет для логов: "glpa…9f".
func Mask(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:4] + "…" + s[len(s)-2:]
}

// ValidateRemoteURL разрешает только http(s) URL без учётных данных и, если
// allowPrivate=false, запрещает адреса внутренней сети (защита от SSRF).
func ValidateRemoteURL(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return nil, errors.New("некорректный URL")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowPrivate {
			return nil, errors.New("разрешены только https-адреса")
		}
	default:
		return nil, errors.New("поддерживаются только https-адреса")
	}
	if u.User != nil {
		return nil, errors.New("не указывайте логин/токен в URL — используйте поле токена")
	}
	if allowPrivate {
		return u, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return nil, fmt.Errorf("хост %q не найден", u.Hostname())
	}
	for _, ip := range ips {
		if isPrivate(ip.IP) {
			return nil, fmt.Errorf("адрес %q ведёт во внутреннюю сеть", u.Hostname())
		}
	}
	return u, nil
}

func isPrivate(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

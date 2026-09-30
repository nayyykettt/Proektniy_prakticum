package secure

import (
	"context"
	"testing"
	"time"
)

func TestEncryptRoundTrip(t *testing.T) {
	b, _ := NewBox([]byte("0123456789abcdef-secret"))
	enc, err := b.Encrypt("glpat-token")
	if err != nil {
		t.Fatal(err)
	}
	if string(enc) == "glpat-token" {
		t.Fatal("токен не зашифрован")
	}
	got, err := b.Decrypt(enc)
	if err != nil || got != "glpat-token" {
		t.Fatalf("got %q, %v", got, err)
	}

	other, _ := NewBox([]byte("another-secret-value!!"))
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("расшифровка чужим ключом должна падать")
	}
}

func TestSession(t *testing.T) {
	b, _ := NewBox([]byte("0123456789abcdef-secret"))
	s := b.NewSession("admin", time.Hour)
	if role, ok := b.ParseSession(s); !ok || role != "admin" {
		t.Fatalf("role=%q ok=%v", role, ok)
	}
	if _, ok := b.ParseSession(s + "x"); ok {
		t.Fatal("подделанная подпись принята")
	}
	if _, ok := b.ParseSession("viewer|99999999999|abc.bad"); ok {
		t.Fatal("неподписанная сессия принята")
	}
	expired := b.NewSession("admin", -time.Minute)
	if _, ok := b.ParseSession(expired); ok {
		t.Fatal("просроченная сессия принята")
	}
	if !b.CheckCSRF(s, b.CSRFToken(s)) || b.CheckCSRF(s, "nope") {
		t.Fatal("CSRF-проверка работает неверно")
	}
}

func TestValidateRemoteURL(t *testing.T) {
	ctx := context.Background()
	bad := []string{"ftp://x.ru/r.git", "https://user:pass@host.ru/r", "not a url", "http://example.com/r.git", "https://127.0.0.1/r.git", "https://localhost/r.git"}
	for _, u := range bad {
		if _, err := ValidateRemoteURL(ctx, u, false); err == nil {
			t.Errorf("%q должен быть отклонён", u)
		}
	}
	if _, err := ValidateRemoteURL(ctx, "http://127.0.0.1:3000/r.git", true); err != nil {
		t.Errorf("в dev-режиме локальный адрес разрешён: %v", err)
	}
}

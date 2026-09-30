package secure

import (
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "pbkdf2-sha256$") || strings.Contains(h, "correct horse") {
		t.Fatalf("неожиданный формат хеша: %q", h)
	}
	h2, _ := HashPassword("correct horse")
	if h == h2 {
		t.Fatal("соль должна делать хеши одного пароля разными")
	}
	if !CheckPassword("correct horse", h) {
		t.Fatal("верный пароль не прошёл проверку")
	}
	for _, bad := range []string{"correct horsE", "", "correct horse "} {
		if CheckPassword(bad, h) {
			t.Fatalf("неверный пароль %q прошёл проверку", bad)
		}
	}
	for _, broken := range []string{"", "plain", "pbkdf2-sha256$x$y$z", "md5$1$a$b"} {
		if CheckPassword("x", broken) {
			t.Fatalf("повреждённый хеш %q принят", broken)
		}
	}
}

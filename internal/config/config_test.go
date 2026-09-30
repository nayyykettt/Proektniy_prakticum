package config

import (
	"bytes"
	"testing"
)

func TestLoadDefaultsAndSecret(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Addr != ":8080" || c.MaxCommits != 20000 || len(c.AppSecret) < 16 {
		t.Fatalf("значения по умолчанию: %+v", c)
	}
	// Секрет сохраняется и переживает перезапуск (иначе токены не расшифровать).
	again, err := Load()
	if err != nil || !bytes.Equal(again.AppSecret, c.AppSecret) {
		t.Fatal("секрет должен быть стабильным между запусками")
	}
}

func TestLoadValidation(t *testing.T) {
	cases := map[string]string{
		"MAX_COMMITS":   "abc",
		"SYNC_INTERVAL": "soon",
		"WORKDAY_START": "20",
		"APP_SECRET":    "short",
		"COOKIE_SECURE": "yes please",
	}
	for k, v := range cases {
		t.Run(k, func(t *testing.T) {
			t.Setenv("DATA_DIR", t.TempDir())
			t.Setenv(k, v)
			if _, err := Load(); err == nil {
				t.Fatalf("%s=%q должно отклоняться", k, v)
			}
		})
	}
}

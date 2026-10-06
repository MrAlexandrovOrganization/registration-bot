package app

import (
	"strings"
	"testing"
)

func TestBotIDFromToken(t *testing.T) {
	t.Setenv("TELEGRAM_LOCAL_API_URL", "")
	t.Setenv("WEBHOOK_URL", "")
	t.Setenv("BACKEND_TOKEN", strings.Repeat("x", 32))
	t.Setenv("ALLOW_INSECURE_GRPC", "true")
	t.Setenv("TOTAL_RATE", "20")
	t.Setenv("BROADCAST_RATE", "15")
	// Obsolete configuration must not override the token's identity.
	t.Setenv("BOT_ID", "999")
	for _, tc := range []struct {
		token string
		id    int64
	}{
		{"7000000001:synthetic-secret", 7000000001},
		{"", 0},
		{"1000", 0},
		{"1000:", 0},
		{"not-a-number:synthetic-secret", 0},
		{"0:synthetic-secret", 0},
		{"-1:synthetic-secret", 0},
		{"+1:synthetic-secret", 0},
		{"001:synthetic-secret", 0},
		{"9223372036854775808:synthetic-secret", 0},
	} {
		t.Setenv("BOT_TOKEN", tc.token)
		c, err := Load()
		if tc.id > 0 {
			if err != nil || c.BotID != tc.id {
				t.Fatalf("token identity: got %d, error %v", c.BotID, err)
			}
		} else if err == nil || err.Error() != "invalid BOT_TOKEN format" {
			t.Fatalf("expected safe token format error, got %v", err)
		}
	}
}

func TestTelegramOrigin(t *testing.T) {
	for _, raw := range []string{"", "http://telegram-bot-api:8081", "https://example.invalid/"} {
		got, err := telegramOrigin(raw)
		if err != nil || got != strings.TrimRight(raw, "/") {
			t.Fatalf("valid origin rejected: %v", err)
		}
	}
	for _, raw := range []string{"http://user:synthetic@host", "http://host/botTOKEN", "http://host?secret=synthetic", "http://host#fragment", "ftp://host", "http://host:99999", "http://host/%2f"} {
		_, err := telegramOrigin(raw)
		if err == nil || strings.Contains(err.Error(), raw) {
			t.Fatal("invalid origin accepted or leaked")
		}
	}
}

func TestWebhookConfiguration(t *testing.T) {
	t.Setenv("TELEGRAM_LOCAL_API_URL", "")
	t.Setenv("BOT_TOKEN", "1000:synthetic-secret")
	t.Setenv("BACKEND_TOKEN", strings.Repeat("x", 32))
	t.Setenv("ALLOW_INSECURE_GRPC", "true")
	t.Setenv("TOTAL_RATE", "20")
	t.Setenv("BROADCAST_RATE", "15")
	t.Setenv("WEBHOOK_LISTEN_ADDR", ":8080")
	for _, tt := range []struct {
		name, url, secret string
		valid             bool
	}{
		{"polling", "", "", true},
		{"webhook", "https://example.test/hook", "fixture-secret", true},
		{"root", "https://example.test", "fixture-secret", true},
		{"http", "http://example.test/hook", "fixture-secret", false},
		{"credentials", "https://user:password@example.test/hook", "fixture-secret", false},
		{"query", "https://example.test/hook?x=1", "fixture-secret", false},
		{"fragment", "https://example.test/hook#x", "fixture-secret", false},
		{"missing_secret", "https://example.test/hook", "", false},
		{"bad_secret", "https://example.test/hook", "fixture space", false},
		{"long_secret", "https://example.test/hook", strings.Repeat("a", 257), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEBHOOK_URL", tt.url)
			t.Setenv("TELEGRAM_WEBHOOK_SECRET", tt.secret)
			c, err := Load()
			if (err == nil) != tt.valid {
				t.Fatalf("validation: %v", err)
			}
			if err == nil && tt.url != "" && c.WebhookPath == "" {
				t.Fatal("missing path")
			}
		})
	}
}

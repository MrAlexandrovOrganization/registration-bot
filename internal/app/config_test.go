package app

import (
	"strings"
	"testing"
)

func TestBotIDFromToken(t *testing.T) {
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

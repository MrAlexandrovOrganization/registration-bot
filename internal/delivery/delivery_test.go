package delivery

import (
	"context"
	"testing"
	"time"

	"registration.local/frontend/internal/telegram"
)

func TestErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err   error
		out   string
		delay int64
	}{{nil, "sent", 0}, {&telegram.APIError{Code: 429, RetryAfter: 31}, "rate_limit", 31}, {&telegram.APIError{Code: 403}, "blocked", 0}, {&telegram.APIError{Code: 400}, "permanent", 0}, {&telegram.APIError{Uncertain: true}, "uncertain", 0}, {&telegram.APIError{Code: 503}, "transient", 0}} {
		out, delay := Classify(tc.err)
		if out != tc.out || delay != tc.delay {
			t.Fatal(out, delay)
		}
	}
}
func TestLimiterSeparatesChatsAndCancels(t *testing.T) {
	l := NewLimiter(20, 15)
	ctx := context.Background()
	if err := l.Wait(ctx, 1, false, true); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := l.Wait(short, 1, false, true); err == nil {
		t.Fatal("per chat limit not applied")
	}
}

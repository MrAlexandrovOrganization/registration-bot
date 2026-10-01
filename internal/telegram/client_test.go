package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"registration.local/frontend/internal/resources"
	"registration.local/frontend/internal/tgfmt"
)

func TestTelegramTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Text string `json:"text"`
			Mode string `json:"parse_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input.Mode != "HTML" || input.Text != "&lt;test&gt;" {
			t.Error(input)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":42}}`))
	}))
	defer server.Close()
	c := &Client{Base: server.URL + "/", HTTP: server.Client()}
	id, err := c.Send(context.Background(), 1, tgfmt.Escape("<test>"), resources.Markup{})
	if err != nil || id != 42 {
		t.Fatal(id, err)
	}
}
func TestRetryAfterAndNoSecretInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":429,"description":"synthetic-secret","parameters":{"retry_after":35}}`))
	}))
	defer server.Close()
	c := &Client{Base: server.URL + "/bot-synthetic-secret/", HTTP: server.Client()}
	err := c.Call(context.Background(), "sendMessage", struct{}{}, nil)
	var api *APIError
	if !errors.As(err, &api) || api.RetryAfter != 35 || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("unsafe or invalid API error")
	}
	server.Close()
	err = c.Call(context.Background(), "sendMessage", struct{}{}, nil)
	if strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("transport leaked URL")
	}
}

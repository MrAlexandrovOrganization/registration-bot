package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"registration.local/frontend/internal/telegram"
)

func TestExplicitWebhookCommandsPreservePendingUpdates(t *testing.T) {
	var methods []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.URL.Path)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload["drop_pending_updates"] != false {
			t.Error("pending updates would be lost")
		}
		if r.URL.Path == "/setWebhook" {
			if payload["url"] != "https://example.test/hook" || payload["secret_token"] != "fixture-secret" || payload["max_connections"] != float64(1) {
				t.Error("registration contract")
			}
			if len(payload["allowed_updates"].([]any)) != 4 {
				t.Error("allowed updates")
			}
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer tg.Close()
	client := &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}
	c := Config{WebhookURL: "https://example.test/hook", WebhookSecret: "fixture-secret"}
	for _, command := range []string{"register-webhook", "delete-webhook"} {
		if err := webhookCommand(t.Context(), client, c, command); err != nil {
			t.Fatal(err)
		}
	}
	if err := webhookCommand(t.Context(), client, c, "unknown"); err == nil {
		t.Fatal("accepted unknown command")
	}
	if err := webhookCommand(t.Context(), client, Config{}, "register-webhook"); err == nil {
		t.Fatal("accepted empty URL")
	}
	if !reflect.DeepEqual(methods, []string{"/setWebhook", "/deleteWebhook"}) {
		t.Fatalf("unexpected calls %v", methods)
	}
}

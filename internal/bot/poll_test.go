package bot

import (
	"encoding/json"
	"testing"

	"registration.local/frontend/internal/resources"
	"registration.local/frontend/internal/telegram"
)

func TestContactOwnership(t *testing.T) {
	var u telegram.Update
	if err := json.Unmarshal([]byte(`{"update_id":7,"message":{"message_id":4,"from":{"id":10},"chat":{"id":10,"type":"private"},"contact":{"user_id":99,"phone_number":"1234567890"}}}`), &u); err != nil {
		t.Fatal(err)
	}
	p := Convert(u)
	if p.Actor != 10 || p.ContactOwner != 99 || p.Id != 7 {
		t.Fatal(p)
	}
}

func TestReplyCancelConversion(t *testing.T) {
	for _, tc := range []struct{ name, chatType, text, want string }{
		{"private reply button", "private", resources.Text("cancel"), "/cancel"},
		{"group text", "supergroup", resources.Text("cancel"), resources.Text("cancel")},
		{"ordinary answer", "private", "Fixture answer", "Fixture answer"},
		{"command", "private", "/cancel", "/cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := telegram.Update{ID: 9, Message: &telegram.Message{ID: 5, From: telegram.User{ID: 10}, Chat: telegram.Chat{ID: 10, Type: tc.chatType}, Text: tc.text}}
			got := Convert(u)
			if got == nil || got.Text != tc.want || got.Callback != "" || got.Kind != "message" {
				t.Fatalf("unexpected reply button conversion: %v", got)
			}
		})
	}
}
func TestUnknownUpdateIgnored(t *testing.T) {
	if Convert(telegram.Update{ID: 1}) != nil {
		t.Fatal("unknown update accepted")
	}
}

func TestPinnedServiceMessageIgnored(t *testing.T) {
	for _, actor := range []int64{1000, 10} {
		data, err := json.Marshal(map[string]any{
			"update_id": 12,
			"message": map[string]any{
				"message_id":     80,
				"from":           map[string]any{"id": actor},
				"chat":           map[string]any{"id": 10, "type": "private"},
				"pinned_message": map[string]any{"message_id": 77, "text": "Fixture reminder"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		var update telegram.Update
		if err := json.Unmarshal(data, &update); err != nil {
			t.Fatal(err)
		}
		if Convert(update) != nil {
			t.Fatal("pin service update must not be submitted as a questionnaire answer")
		}
		// The shared ingress must acknowledge unsupported updates without any RPC.
		if err := (&Poller{}).Accept(t.Context(), update); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStartPayloadPreserved(t *testing.T) {
	var u telegram.Update
	if err := json.Unmarshal([]byte(`{"update_id":8,"message":{"message_id":5,"from":{"id":10},"chat":{"id":10,"type":"private"},"text":"/start campaign_1"}}`), &u); err != nil {
		t.Fatal(err)
	}
	p := Convert(u)
	if p.Text != "/start campaign_1" || p.Actor != 10 || p.ChatType != "private" {
		t.Fatal(p)
	}
}

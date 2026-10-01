package bot

import (
	"encoding/json"
	"testing"

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
func TestUnknownUpdateIgnored(t *testing.T) {
	if Convert(telegram.Update{ID: 1}) != nil {
		t.Fatal("unknown update accepted")
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

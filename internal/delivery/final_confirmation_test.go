package delivery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/resources"
	"registration.local/frontend/internal/telegram"
)

func TestFinalConfirmationCopiesWithButtons(t *testing.T) {
	for _, preview := range []bool{false, true} {
		callback := "final:yes"
		negative := "final:no"
		kind := "notice"
		if preview {
			callback, negative, kind = "preview_final_0", "preview_final_1", "final_preview"
		}
		var received atomic.Bool
		tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			received.Store(true)
			var body struct {
				Chat    int64            `json:"chat_id"`
				Source  int64            `json:"from_chat_id"`
				Message int64            `json:"message_id"`
				Markup  resources.Markup `json:"reply_markup"`
				Text    string           `json:"text"`
				Caption string           `json:"caption"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.URL.Path != "/copyMessage" || body.Chat != 12 || body.Source != 1 || body.Message != 777 || body.Text != "" || body.Caption != "" {
				t.Error("forwarded message must be copied without replacing its content", body)
			}
			if len(body.Markup.InlineKeyboard) != 2 || body.Markup.InlineKeyboard[0][0].Text != "Буду <&> ✅" || body.Markup.InlineKeyboard[1][0].Text != "Пропущу 😢" || body.Markup.InlineKeyboard[0][0].CallbackData != callback {
				t.Error("missing confirmation buttons", body.Markup)
			}
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":888}}`))
		}))
		backend := &fakeBackend{job: &pb.Delivery{Id: 11, Lease: "fixture", Chat: 12, Kind: "copy", SourceChat: 1, SourceMessage: 777, View: &pb.View{Kind: kind, Code: "final_invitation", Buttons: []*pb.Button{
			{Label: "Буду <&> ✅", LabelKey: "final_yes", Data: callback}, {Label: "Пропущу 😢", LabelKey: "final_no", Data: negative},
		}}}}
		s := &Sender{Backend: backend, Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_final_copy", Help: "test"}, []string{"outcome"})}
		if err := s.handleID(t.Context(), "worker", 11, false); err != nil {
			t.Fatal(err)
		}
		tg.Close()
		if !received.Load() || backend.completion.Outcome != "sent" || backend.completion.TelegramMessageId != 888 {
			t.Fatal("copy completion lost the preview message ID")
		}
	}
}

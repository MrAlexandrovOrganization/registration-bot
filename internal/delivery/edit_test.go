package delivery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/kafka-go"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/telegram"
)

func TestEditDeliveryOutcomes(t *testing.T) {
	for _, tt := range []struct {
		name, description string
		code              int
		outcome           string
		fallback          bool
		id                int64
	}{
		{"edited", "", 200, "sent", false, 42},
		{"not_modified", "Bad Request: message is not modified: specified new message content and reply markup are exactly the same", 400, "sent", false, 42},
		{"missing", "Bad Request: message to edit not found", 400, "sent", true, 77},
		{"uneditable", "Bad Request: message can't be edited", 400, "sent", true, 77},
		{"other_bad_request", "Bad Request: can't parse entities", 400, "permanent", false, 0},
		{"rate_limit", "Too Many Requests", 429, "rate_limit", false, 0},
		{"server_error", "Internal Server Error", 500, "transient", false, 0},
		{"ambiguous", "", 0, "uncertain", false, 0},
		{"network", "", -1, "uncertain", false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var methods []string
			var mu sync.Mutex
			var editAt time.Time
			tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				methods = append(methods, r.URL.Path)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["parse_mode"] != "HTML" || !strings.Contains(body["text"].(string), "&lt;Alice&gt;") {
					t.Error("missing HTML escaping")
				}
				if r.URL.Path == "/sendMessage" {
					if time.Since(editAt) < 900*time.Millisecond {
						t.Error("fallback bypassed chat limiter")
					}
					_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
					return
				}
				editAt = time.Now()
				if body["message_id"] != float64(42) {
					t.Error("wrong edit target")
				}
				markup := body["reply_markup"].(map[string]any)
				if _, ok := markup["remove_keyboard"]; ok {
					t.Error("reply markup sent to edit")
				}
				if _, ok := markup["inline_keyboard"]; !ok {
					t.Error("missing keyboard removal")
				}
				if tt.code == -1 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				if tt.code == 0 {
					_, _ = w.Write([]byte(`broken response`))
					return
				}
				w.WriteHeader(tt.code)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": tt.code == 200, "result": map[string]int{"message_id": 42}, "error_code": tt.code, "description": tt.description})
			}))
			defer tg.Close()
			backend := &fakeBackend{job: &pb.Delivery{Id: 11, Lease: "fixture", Chat: 12, Kind: "view", EditMessageId: 42, View: &pb.View{Kind: "confirm", Fields: []*pb.Field{{Key: "name", Value: "<Alice>"}}}}}
			s := &Sender{Backend: backend, Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_delivery", Help: "test"}, []string{"outcome"})}
			if err := s.handle(t.Context(), "worker", fetched{message: kafka.Message{Value: []byte("11")}}); err != nil {
				t.Fatal(err)
			}
			want := []string{"/editMessageText"}
			mu.Lock()
			defer mu.Unlock()
			if tt.fallback {
				want = append(want, "/sendMessage")
			}
			if !reflect.DeepEqual(methods, want) {
				t.Fatalf("methods %v", methods)
			}
			if backend.completion.Outcome != tt.outcome || backend.completion.TelegramMessageId != tt.id {
				t.Fatalf("completion %v", backend.completion)
			}
		})
	}
}

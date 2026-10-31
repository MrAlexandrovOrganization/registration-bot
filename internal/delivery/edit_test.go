package delivery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/kafka-go"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/resources"
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

func TestPhoneReplyKeyboardDelivery(t *testing.T) {
	for _, tc := range []struct {
		name string
		view *pb.View
	}{
		{"show", &pb.View{Kind: "question", Field: "phone", Buttons: []*pb.Button{{LabelKey: "cancel", Data: "c:2:cancel"}}}},
		{"cancel", &pb.View{Kind: "notice", Code: "edit_cancelled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var received atomic.Bool
			tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received.Store(true)
				var body struct {
					Text   string           `json:"text"`
					Markup resources.Markup `json:"reply_markup"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if r.URL.Path != "/sendMessage" || len(body.Markup.InlineKeyboard) != 0 {
					t.Error("reply keyboard changes require sendMessage without inline markup")
				}
				if tc.name == "show" {
					if body.Text != resources.Text("question_phone") || len(body.Markup.Keyboard) != 2 || !body.Markup.Keyboard[0][0].RequestContact || body.Markup.Keyboard[1][0].Text != resources.Text("cancel") || body.Markup.Keyboard[1][0].CallbackData != "" {
						t.Error("expected one phone question with contact-sharing and cancel reply buttons")
					}
				} else if !body.Markup.Remove || len(body.Markup.Keyboard) != 0 {
					t.Error("cancellation must send remove_keyboard to Telegram")
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":77}}`))
			}))
			defer tg.Close()
			backend := &fakeBackend{job: &pb.Delivery{Id: 11, Lease: "fixture", Chat: 12, Kind: "view", View: tc.view}}
			s := &Sender{Backend: backend, Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_phone_delivery", Help: "test"}, []string{"outcome"})}
			if err := s.handleID(t.Context(), "worker", 11, false); err != nil {
				t.Fatal(err)
			}
			if !received.Load() || backend.completion.Outcome != "sent" {
				t.Fatal("keyboard change was not delivered")
			}
		})
	}
}

func TestSurveyReplyShowsOnlySavedQuestionnaire(t *testing.T) {
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.URL.Path != "/sendMessage" || !strings.Contains(body.Text, resources.Text("registered_title")) || strings.Contains(body.Text, "agreement_1") || strings.Contains(body.Text, "private answer") {
			t.Error("expected a separate questionnaire without survey data", body.Text)
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":700}}`))
	}))
	defer tg.Close()
	backend := &fakeBackend{job: &pb.Delivery{Id: 11, Lease: "fixture", Chat: 42, Kind: "view", View: &pb.View{Kind: "registered", Fields: []*pb.Field{{Key: "name", Value: "Synthetic"}, {Key: "survey_1", Value: "private answer"}}}}}
	s := &Sender{Backend: backend, Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_survey_delivery", Help: "test"}, []string{"outcome"})}
	if err := s.handleID(t.Context(), "worker", 11, false); err != nil {
		t.Fatal(err)
	}
	if backend.completion.Outcome != "sent" {
		t.Fatal(backend.completion)
	}
}

func TestPhotoQuestionDelivery(t *testing.T) {
	for _, mode := range []string{"send", "clear", "already_cleared"} {
		t.Run(mode, func(t *testing.T) {
			tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if mode == "send" {
					var caption, photo string
					_ = json.Unmarshal(body["caption"], &caption)
					_ = json.Unmarshal(body["photo"], &photo)
					if r.URL.Path != "/sendPhoto" || photo != "fixture-photo" || caption != "&lt;Фото&gt; &amp; вопрос?" {
						t.Error("incorrect photo delivery", r.URL.Path, caption, photo)
					}
				} else {
					var markup struct {
						Buttons [][]resources.Button `json:"inline_keyboard"`
					}
					_ = json.Unmarshal(body["reply_markup"], &markup)
					if r.URL.Path != "/editMessageReplyMarkup" || markup.Buttons == nil || len(markup.Buttons) != 0 {
						t.Error("photo buttons were not cleared")
					}
					if _, ok := body["caption"]; ok {
						t.Error("caption must remain unchanged")
					}
					if mode == "already_cleared" {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"ok":false,"error_code":400,"description":"Bad Request: message is not modified"}`))
						return
					}
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":700}}`))
			}))
			defer tg.Close()
			job := &pb.Delivery{Id: 11, Chat: 42, Lease: "fixture", Kind: "view", View: &pb.View{Kind: "survey_broadcast", Title: "Metadata title", Text: "<Фото> & вопрос?", PhotoFileId: "fixture-photo", Buttons: []*pb.Button{{Label: "Да", Data: "qa_1_2"}}}}
			if mode != "send" {
				job.Kind = "survey_remove_keyboard"
				job.SourceMessage = 700
			}
			backend := &fakeBackend{job: job}
			s := &Sender{Backend: backend, Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_photo_delivery", Help: "test"}, []string{"outcome"})}
			if err := s.handleID(t.Context(), "worker", 11, false); err != nil {
				t.Fatal(err)
			}
			if backend.completion.Outcome != "sent" || backend.completion.TelegramMessageId != 700 {
				t.Fatal(backend.completion)
			}
		})
	}
}

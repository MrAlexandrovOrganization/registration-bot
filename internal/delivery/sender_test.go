package delivery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/telegram"
)

type fakeBackend struct {
	pb.RegistrationClient
	job        *pb.Delivery
	completion *pb.Completion
}

func (f *fakeBackend) Claim(_ context.Context, r *pb.ClaimRequest, _ ...grpc.CallOption) (*pb.Delivery, error) {
	if f.completion != nil {
		return &pb.Delivery{}, nil
	}
	return f.job, nil
}
func (f *fakeBackend) Complete(_ context.Context, r *pb.Completion, _ ...grpc.CallOption) (*pb.Receipt, error) {
	f.completion = r
	return &pb.Receipt{}, nil
}
func TestSenderRateLimitAndDuplicate(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(429)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 429, "parameters": map[string]int{"retry_after": 30}})
	}))
	defer server.Close()
	backend := &fakeBackend{job: &pb.Delivery{Id: 11, Lease: "owned", Chat: 123, Kind: "view", View: &pb.View{Kind: "notice", Code: "saved"}}}
	sender := &Sender{Backend: backend, Telegram: &telegram.Client{Base: server.URL + "/", HTTP: server.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_delivery_total", Help: "test"}, []string{"outcome"})}
	message := fetched{message: kafka.Message{Value: []byte(strconv.Itoa(11))}, bulk: true}
	if err := sender.handle(context.Background(), "fixture-worker", message); err != nil {
		t.Fatal(err)
	}
	if backend.completion == nil || backend.completion.Outcome != "rate_limit" || backend.completion.RetryAfterSeconds != 30 || backend.completion.Lease != "owned" {
		t.Fatal("retry not persisted before completion")
	}
	if err := sender.handle(context.Background(), "fixture-worker", message); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("duplicate Kafka message caused another HTTP send")
	}
}
func TestSenderPersistedCooldownPreventsHTTP(t *testing.T) {
	backend := &fakeBackend{job: &pb.Delivery{NotBeforeUnixMs: time.Now().Add(time.Minute).UnixMilli()}}
	sender := &Sender{Backend: backend, Ready: func(bool) {}}
	if err := sender.handle(context.Background(), "fixture-worker", fetched{message: kafka.Message{Value: []byte("11")}}); err != nil {
		t.Fatal(err)
	}
	if backend.completion != nil {
		t.Fatal("unclaimed job completed")
	}
}

func TestSenderRegistrationPin(t *testing.T) {
	for _, tc := range []struct {
		code    int
		outcome string
	}{{200, "sent"}, {429, "rate_limit"}, {500, "transient"}, {403, "permanent"}} {
		t.Run(strconv.Itoa(tc.code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body struct {
					Chat    int64 `json:"chat_id"`
					Message int64 `json:"message_id"`
					Silent  bool  `json:"disable_notification"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.URL.Path != "/pinChatMessage" || body.Chat != 123 || body.Message != 777 || !body.Silent {
					t.Error("incorrect pin request")
				}
				w.WriteHeader(tc.code)
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": tc.code == 200, "result": true, "error_code": tc.code, "parameters": map[string]int{"retry_after": 30}})
			}))
			defer server.Close()
			backend := &fakeBackend{job: &pb.Delivery{Id: 11, Lease: "owned", Chat: 123, Kind: "pin", SourceMessage: 777}}
			sender := &Sender{Backend: backend, Telegram: &telegram.Client{Base: server.URL + "/", HTTP: server.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_pin_total", Help: "test"}, []string{"outcome"})}
			if err := sender.handleID(context.Background(), "fixture-worker", 11, false); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || backend.completion.Outcome != tc.outcome || backend.completion.TelegramMessageId != 777 {
				t.Fatal("pin result must be persisted without sending another message")
			}
			if tc.code == 429 && backend.completion.RetryAfterSeconds != 30 {
				t.Fatal("lost pin retry delay")
			}
		})
	}
}

package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/telegram"
)

type acceptBackend struct {
	pb.RegistrationClient
	accept func(context.Context, *pb.Update) (*pb.Receipt, error)
}

func (b acceptBackend) Accept(ctx context.Context, u *pb.Update, _ ...grpc.CallOption) (*pb.Receipt, error) {
	return b.accept(ctx, u)
}
func testPoller(b pb.RegistrationClient) *Poller {
	return &Poller{Backend: b, BotID: 1000, Ready: func(bool) {}, Updates: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_updates", Help: "test"}, []string{"result"})}
}
func hookRequest(body, secret, method string) *http.Request {
	r := httptest.NewRequest(method, "/hook", strings.NewReader(body))
	r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
	return r
}

const validUpdate = `{"update_id":8,"message":{"message_id":1,"from":{"id":12},"chat":{"id":12,"type":"private"},"text":"/start"}}`

type recordingHints struct{ ids []int64 }

func (h *recordingHints) Offer(ids []int64) { h.ids = append(h.ids, ids...) }

func TestWebhookValidationAndOverload(t *testing.T) {
	h := NewWebhook(t.Context(), nil, "fixture-secret") // deliberately no consumer
	for _, tt := range []struct {
		name, body, secret, method string
		code                       int
	}{
		{"method", validUpdate, "fixture-secret", "GET", 405},
		{"secret", validUpdate, "wrong", "POST", 401},
		{"missing", validUpdate, "", "POST", 401},
		{"invalid", `{`, "fixture-secret", "POST", 400},
		{"trailing", validUpdate + ` {}`, "fixture-secret", "POST", 400},
		{"oversized", `{"update_id":1,"padding":"` + strings.Repeat("x", 1<<20) + `"}`, "fixture-secret", "POST", 413},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, hookRequest(tt.body, tt.secret, tt.method))
			if w.Code != tt.code {
				t.Fatalf("status %d", w.Code)
			}
		})
	}
	for range cap(h.queue) {
		h.queue <- webhookJob{}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, hookRequest(validUpdate, "fixture-secret", "POST"))
	if w.Code != 503 {
		t.Fatalf("queue overload: %d", w.Code)
	}
}

func TestWebhookWaitsForDurableAcceptAndReturnsRetryOnFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "failure"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			p := testPoller(acceptBackend{accept: func(ctx context.Context, u *pb.Update) (*pb.Receipt, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if fail {
					return nil, status.Error(codes.Unavailable, "fixture")
				}
				return &pb.Receipt{Duplicate: true, DeliveryIds: []int64{1, 2}}, nil
			}})
			hints := &recordingHints{}
			p.Hints = hints
			h := NewWebhook(ctx, p, "fixture-secret")
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); h.Run() }()
			w := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { defer close(done); h.ServeHTTP(w, hookRequest(validUpdate, "fixture-secret", "POST")) }()
			<-entered
			select {
			case <-done:
				t.Fatal("acknowledged before commit")
			default:
			}
			close(release)
			<-done
			want := 200
			if fail {
				want = 503
			}
			if w.Code != want {
				t.Fatalf("status %d", w.Code)
			}
			if !fail && !reflect.DeepEqual(hints.ids, []int64{1, 2}) {
				t.Fatalf("duplicate receipt not replayed: %v", hints.ids)
			}
			if fail && len(hints.ids) != 0 {
				t.Fatal("scheduled failed acceptance")
			}
			cancel()
			<-workerDone
		})
	}
}

func TestWebhookCancellationStopsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	p := testPoller(acceptBackend{accept: func(ctx context.Context, _ *pb.Update) (*pb.Receipt, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	h := NewWebhook(ctx, p, "fixture-secret")
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); h.Run() }()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(w, hookRequest(validUpdate, "fixture-secret", "POST")) }()
	<-entered
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("HTTP did not stop")
	}
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if w.Code != 503 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestCallbackEditAttestation(t *testing.T) {
	for _, tt := range []struct {
		name, extra  string
		sender, date int
		text, inline string
		want         bool
	}{
		{"owned", "", 1000, 1, "question", "", true},
		{"user", "", 12, 1, "question", "", false},
		{"inaccessible", "", 1000, 0, "question", "", false},
		{"no_text", "", 1000, 1, "", "", false},
		{"media", `,"photo":[]`, 1000, 1, "question", "", false},
		{"inline", "", 1000, 1, "question", "inline-id", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got *pb.Update
			p := testPoller(acceptBackend{accept: func(_ context.Context, u *pb.Update) (*pb.Receipt, error) { got = u; return &pb.Receipt{}, nil }})
			tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got == nil {
					t.Error("spinner before persistence")
				}
				if r.URL.Path != "/answerCallbackQuery" {
					t.Error("unexpected content send")
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
			}))
			defer tg.Close()
			p.Telegram = &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}
			body, _ := json.Marshal(map[string]any{"update_id": 1, "callback_query": map[string]any{"id": "cb", "from": map[string]int{"id": 12}, "data": "edit:1", "inline_message_id": tt.inline, "message": map[string]any{"message_id": 99, "date": tt.date, "from": map[string]int{"id": tt.sender}, "chat": map[string]any{"id": 12, "type": "private"}, "text": tt.text}}})
			// Add a media field to the otherwise valid text-message fixture.
			if tt.extra != "" {
				body = []byte(strings.Replace(string(body), `"message_id":99`, `"message_id":99`+tt.extra, 1))
			}
			var raw telegram.Update
			if err := json.Unmarshal(body, &raw); err != nil {
				t.Fatal(err)
			}
			if err := p.Accept(t.Context(), raw); err != nil {
				t.Fatal(err)
			}
			if got.CallbackMessageEditable != tt.want || got.MessageId != 99 {
				t.Fatalf("attestation %v, id %d", got.CallbackMessageEditable, got.MessageId)
			}
		})
	}
}

package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/telegram"
)

type flakyBackend struct {
	pb.RegistrationClient
	mu        sync.Mutex
	attempts  int
	persisted bool
}

func (f *flakyBackend) Accept(_ context.Context, u *pb.Update, _ ...grpc.CallOption) (*pb.Receipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts == 1 {
		return nil, status.Error(codes.Unavailable, "fixture outage")
	}
	f.persisted = true
	return &pb.Receipt{}, nil
}
func TestPollingAcknowledgesOnlyAfterPersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &flakyBackend{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/getMe" {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1000}}`))
			return
		}
		var request struct {
			Offset int64 `json:"offset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request.Offset == 0 {
			_, _ = w.Write([]byte(`{"ok":true,"result":[{"update_id":8,"message":{"message_id":1,"from":{"id":12},"chat":{"id":12,"type":"private"},"text":"/start"}}]}`))
			return
		}
		backend.mu.Lock()
		persisted := backend.persisted
		backend.mu.Unlock()
		if !persisted || request.Offset != 9 {
			t.Error("acknowledged unpersisted update")
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":[]}`))
		cancel()
	}))
	defer server.Close()
	p := &Poller{Telegram: &telegram.Client{Base: server.URL + "/", HTTP: server.Client()}, Backend: backend, BotID: 1000, Ready: func(bool) {}, Updates: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "test_updates_total", Help: "test"}, []string{"result"})}
	if err := p.Run(ctx); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.attempts != 2 {
		t.Fatal("did not retry durable acceptance", backend.attempts)
	}
}

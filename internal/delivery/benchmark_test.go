package delivery

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/telegram"
)

type benchmarkDeliveryBackend struct {
	pb.RegistrationClient
	oneChat bool
}

func (f benchmarkDeliveryBackend) Claim(ctx context.Context, r *pb.ClaimRequest, _ ...grpc.CallOption) (*pb.Delivery, error) {
	if err := Sleep(ctx, 2*time.Millisecond); err != nil {
		return nil, err
	}
	chat := r.Id
	if f.oneChat {
		chat = 42
	}
	return &pb.Delivery{Id: r.Id, Chat: chat, Kind: "view", Lease: "fixture", View: &pb.View{Kind: "help_public"}}, nil
}

func (f benchmarkDeliveryBackend) Complete(ctx context.Context, _ *pb.Completion, _ ...grpc.CallOption) (*pb.Receipt, error) {
	return &pb.Receipt{}, Sleep(ctx, 2*time.Millisecond)
}

// Sequential sender cycle with real limiter, fake 2ms RPCs and fake Telegram HTTP.
// Measures completed messages, not admitted updates, and never contacts Telegram.
func BenchmarkDelivery(b *testing.B) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer slog.SetDefault(old)
	for _, tc := range []struct {
		name          string
		latency       time.Duration
		oneChat, bulk bool
	}{
		{"distinct-chats", 0, false, false},
		{"distinct-chats", 70 * time.Millisecond, false, false},
		{"one-chat", 70 * time.Millisecond, true, false},
		{"broadcast", 0, false, true},
	} {
		b.Run(fmt.Sprintf("%s/telegram=%s", tc.name, tc.latency), func(b *testing.B) {
			tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/sendMessage" {
					b.Error("unexpected Telegram method")
				}
				if err := Sleep(r.Context(), tc.latency); err != nil {
					return
				}
				_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55}}`))
			}))
			defer tg.Close()
			s := &Sender{Backend: benchmarkDeliveryBackend{oneChat: tc.oneChat}, Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "bench_outcome", Help: "fixture"}, []string{"outcome"})}
			// Warm-up also reserves the initial limiter slot, avoiding burst bias.
			if err := s.handleID(b.Context(), "fixture-worker", 1, tc.bulk); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			start := time.Now()
			for i := 0; i < b.N; i++ {
				if err := s.handleID(b.Context(), "fixture-worker", int64(i+2), tc.bulk); err != nil {
					b.Fatal(err)
				}
			}
			elapsed := time.Since(start)
			b.StopTimer()
			b.ReportMetric(float64(b.N)/elapsed.Seconds(), "messages/s")
		})
	}
}

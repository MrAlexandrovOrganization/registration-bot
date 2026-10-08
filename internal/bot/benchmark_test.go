package bot

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	pb "registration.local/frontend/api"
)

type benchmarkBackend struct {
	pb.RegistrationClient
	delay time.Duration
}

func (f benchmarkBackend) Accept(ctx context.Context, _ *pb.Update, _ ...grpc.CallOption) (*pb.Receipt, error) {
	if f.delay > 0 {
		timer := time.NewTimer(f.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &pb.Receipt{}, nil
}

// HTTP/JSON/admission baseline. Fake Accept latency is explicit; no real backend,
// Telegram, sender or gRPC transport is involved in these ingress measurements.
func BenchmarkWebhook(b *testing.B) {
	for _, delay := range []time.Duration{0, 5 * time.Millisecond, 20 * time.Millisecond} {
		for _, concurrency := range []int{1, 16} {
			b.Run(fmt.Sprintf("backend=%s/clients=%d", delay, concurrency), func(b *testing.B) {
				ctx, cancel := context.WithCancel(b.Context())
				p := &Poller{Backend: benchmarkBackend{delay: delay}, Ready: func(bool) {}, Updates: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "bench_updates", Help: "fixture"}, []string{"result"})}
				hook := NewWebhook(ctx, p, "fixture-secret")
				done := make(chan struct{})
				go func() { defer close(done); hook.Run() }()
				defer func() { cancel(); <-done }()
				server := httptest.NewServer(hook)
				defer server.Close()
				client := server.Client()
				client.Timeout = 25 * time.Second
				latency := make([]time.Duration, b.N)
				var next, failures atomic.Int64
				var workers sync.WaitGroup
				b.ResetTimer()
				start := time.Now()
				for range concurrency {
					workers.Go(func() {
						for {
							i := next.Add(1) - 1
							if i >= int64(b.N) {
								return
							}
							t := time.Now()
							body := fmt.Sprintf(`{"update_id":%d,"message":{"message_id":1,"from":{"id":42},"chat":{"id":42,"type":"private"},"text":"/start"}}`, i+1)
							req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL, strings.NewReader(body))
							req.Header.Set("X-Telegram-Bot-Api-Secret-Token", "fixture-secret")
							response, err := client.Do(req)
							if err != nil {
								failures.Add(1)
							} else {
								_, _ = io.Copy(io.Discard, response.Body)
								_ = response.Body.Close()
								if response.StatusCode != http.StatusOK {
									failures.Add(1)
								}
							}
							latency[i] = time.Since(t)
						}
					})
				}
				workers.Wait()
				elapsed := time.Since(start)
				b.StopTimer()
				slices.Sort(latency)
				b.ReportMetric(float64(b.N)/elapsed.Seconds(), "requests/s")
				b.ReportMetric(float64(latency[(len(latency)-1)*95/100])/float64(time.Millisecond), "p95-ms")
				b.ReportMetric(float64(failures.Load()), "errors")
				if failures.Load() != 0 {
					b.Fatal("webhook failures under load")
				}
			})
		}
	}
}

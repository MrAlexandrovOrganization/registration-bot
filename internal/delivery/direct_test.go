package delivery

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/telegram"
)

type directBackend struct {
	pb.UnimplementedRegistrationServer
	mu           sync.Mutex
	jobs         map[int64]*pb.Delivery
	completed    map[int64]bool
	reports      chan int64
	recoverCalls int
	workers      map[string]bool
}

func (b *directBackend) PendingInteractive(_ context.Context, r *pb.PendingInteractiveRequest) (*pb.Receipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.recoverCalls++
	if r.Limit != 100 {
		return nil, status.Error(codes.InvalidArgument, "discovery must be bounded")
	}
	// Recovery must survive failures and discover continuation after Complete.
	if b.recoverCalls == 1 {
		return nil, status.Error(codes.Unavailable, "fixture outage")
	}
	ids := []int64{}
	for _, id := range []int64{2, 3} {
		if b.jobs[id] != nil && !b.completed[id] {
			ids = append(ids, id)
		}
	}
	return &pb.Receipt{DeliveryIds: ids}, nil
}
func (b *directBackend) Claim(_ context.Context, r *pb.ClaimRequest) (*pb.Delivery, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workers[r.Worker] = true
	if r.Id == 0 || b.completed[r.Id] || b.jobs[r.Id] == nil {
		return &pb.Delivery{}, nil
	}
	return b.jobs[r.Id], nil
}
func (b *directBackend) Complete(_ context.Context, r *pb.Completion) (*pb.Receipt, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.Outcome != "sent" || r.Lease != "fixture" {
		return nil, status.Error(codes.InvalidArgument, "invalid completion")
	}
	b.completed[r.Id] = true
	if r.Id == 2 {
		b.jobs[3] = fixtureJob(3)
	}
	b.reports <- r.Id
	return &pb.Receipt{}, nil
}
func fixtureJob(id int64) *pb.Delivery {
	return &pb.Delivery{Id: id, Chat: 12, Lease: "fixture", Kind: "view", View: &pb.View{Kind: "help_public"}}
}

func TestSuppressedQueuedReplyDoesNotCallTelegram(t *testing.T) {
	backend := &fakeBackend{job: &pb.Delivery{}}
	// No Telegram client/limiter: any attempted send or edit fails the test.
	s := &Sender{Backend: backend, Ready: func(bool) {}}
	if err := s.handleID(t.Context(), "worker", 11, false); err != nil {
		t.Fatal(err)
	}
	if backend.completion != nil {
		t.Fatal("sender completed a reply already cancelled by Claim")
	}
}

type stalledKafka struct {
	fetch      bool
	committing chan struct{}
	once       sync.Once
}

func (k *stalledKafka) FetchMessage(ctx context.Context) (kafka.Message, error) {
	if !k.fetch {
		k.fetch = true
		return kafka.Message{Value: []byte("1")}, nil
	}
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}
func (k *stalledKafka) CommitMessages(ctx context.Context, _ ...kafka.Message) error {
	k.once.Do(func() { close(k.committing) })
	<-ctx.Done()
	return ctx.Err()
}

func TestDirectRecoveryContinuesWhileKafkaCommitIsUnavailable(t *testing.T) {
	b := &directBackend{jobs: map[int64]*pb.Delivery{1: fixtureJob(1), 2: fixtureJob(2), 4: fixtureJob(4)}, completed: make(map[int64]bool), reports: make(chan int64, 10), workers: make(map[string]bool)}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterRegistrationServer(server, b)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sendMessage" {
			t.Error("unexpected Telegram method")
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":55}}`))
	}))
	defer tg.Close()
	k := &stalledKafka{committing: make(chan struct{})}
	s := &Sender{Backend: pb.NewRegistrationClient(conn), Telegram: &telegram.Client{Base: tg.URL + "/", HTTP: tg.Client()}, Limiter: NewLimiter(20, 15), Worker: "one-worker", Hints: NewHints(), reader: k, Ready: func(bool) {}, Outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "fixture_outcomes", Help: "test"}, []string{"outcome"})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	select {
	case <-k.committing:
	case <-time.After(3 * time.Second):
		t.Fatal("broadcast not delivered")
	}
	// Immediate receipt hints (including duplicates) share the sender with recovery.
	s.Hints.Offer([]int64{4, 4, 4})
	seen := map[int64]bool{}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for len(seen) < 4 {
		select {
		case id := <-b.reports:
			if seen[id] {
				t.Fatal("duplicate send")
			}
			seen[id] = true
		case <-deadline.C:
			t.Fatalf("direct/recovery blocked: %v", seen)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("sender did not stop")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.workers) != 1 || !b.workers["one-worker"] || b.recoverCalls < 3 {
		t.Fatalf("worker or recovery mismatch: %v, %d", b.workers, b.recoverCalls)
	}
}

func TestHintsBoundedDeduplicatedAndRecoveryHasCapacity(t *testing.T) {
	h := NewHints()
	for id := int64(1); id <= 1000; id++ {
		h.Offer([]int64{id, id})
	}
	if len(h.queue) != 100 || len(h.ids) != 100 {
		t.Fatal("fast path unbounded")
	}
	h.offer([]int64{1, 1001}, h.recovery)
	if len(h.recovery) != 1 {
		t.Fatal("recovery starved or duplicate accepted")
	}
	id := <-h.queue
	h.Offer([]int64{id})
	if len(h.queue) != 99 {
		t.Fatal("in-flight duplicate queued")
	}
	h.done(id)
	h.Offer([]int64{id})
	if len(h.queue) != 100 {
		t.Fatal("retry lost after completion")
	}
}

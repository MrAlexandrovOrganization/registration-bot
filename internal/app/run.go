package app

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/bot"
	"registration.local/frontend/internal/delivery"
	"registration.local/frontend/internal/telegram"
)

func Run(ctx context.Context, c Config) error {
	var creds credentials.TransportCredentials = insecure.NewCredentials()
	if !c.Insecure {
		var err error
		creds, err = credentials.NewClientTLSFromFile(c.CA, "")
		if err != nil {
			return errors.New("cannot load gRPC CA")
		}
	}
	conn, err := grpc.NewClient(c.Backend, grpc.WithTransportCredentials(creds), grpc.WithStatsHandler(otelgrpc.NewClientHandler()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16<<20)), grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.BackendToken)
		bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return invoker(bounded, method, req, reply, cc, opts...)
	}))
	if err != nil {
		return errors.New("invalid backend address")
	}
	defer conn.Close()
	backend := pb.NewRegistrationClient(conn)
	worker := rand.Text()
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, err = backend.Claim(call, &pb.ClaimRequest{Worker: worker})
	cancel()
	if err != nil {
		return errors.New("backend unavailable or another sender is active")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	updates := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "registration_telegram_updates_total", Help: "Incoming update results"}, []string{"result"})
	outcomes := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "registration_telegram_delivery_total", Help: "Delivery attempt results"}, []string{"outcome"})
	registry.MustRegister(updates, outcomes)
	var polling, sending atomic.Int64
	ready := func(a *atomic.Int64) func(bool) {
		return func(ok bool) {
			if ok {
				a.Store(time.Now().Unix())
			} else {
				a.Store(0)
			}
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if time.Now().Unix()-polling.Load() > 90 || time.Now().Unix()-sending.Load() > 90 {
			http.Error(w, "dependencies unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})
	server := &http.Server{Addr: c.HTTP, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	tg := telegram.New(c.Token)
	limiter := delivery.NewLimiter(c.TotalRate, c.BulkRate)
	run, stop := context.WithCancel(ctx)
	defer stop()
	var workers sync.WaitGroup
	errorsCh := make(chan error, 3)
	workers.Go(func() {
		errorsCh <- (&bot.Poller{Telegram: tg, Backend: backend, BotID: c.BotID, Updates: updates, Ready: ready(&polling)}).Run(run)
	})
	workers.Go(func() {
		errorsCh <- (&delivery.Sender{Backend: backend, Telegram: tg, Limiter: limiter, Brokers: c.Brokers, Prefix: c.Prefix, Group: c.Group, Worker: worker, Outcomes: outcomes, Ready: ready(&sending)}).Run(run)
	})
	go func() { errorsCh <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err = <-errorsCh:
	}
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
	workers.Wait()
	return err
}

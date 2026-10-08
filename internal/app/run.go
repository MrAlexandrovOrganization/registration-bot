package app

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
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
	run, stop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	// Also release after a lost startup Claim response or an early startup failure.
	// Registered after conn.Close so the connection remains usable for cleanup.
	defer stopAndReleaseSender(ctx, stop, &workers, backend, worker)
	if err := waitForSender(ctx, backend, worker); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewGoCollector(), prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	updates := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "registration_telegram_updates_total", Help: "Incoming update results"}, []string{"result"})
	outcomes := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "registration_telegram_delivery_total", Help: "Delivery attempt results"}, []string{"outcome"})
	registry.MustRegister(updates, outcomes)
	var polling, sending atomic.Int64
	sending.Store(time.Now().Unix()) // Startup Claim has already verified ownership.
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
		if (c.WebhookURL == "" && time.Now().Unix()-polling.Load() > 90) || time.Now().Unix()-sending.Load() > 90 {
			http.Error(w, "dependencies unavailable", 503)
			return
		}
		w.WriteHeader(200)
	})
	server := &http.Server{Addr: c.HTTP, Handler: mux, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	tg := telegram.New(c.Token, c.TelegramLocalAPI)
	limiter := delivery.NewLimiter(c.TotalRate, c.BulkRate)
	errorsCh := make(chan error, 4)
	hints := delivery.NewHints()
	poller := &bot.Poller{Telegram: tg, Backend: backend, BotID: c.BotID, Updates: updates, Ready: ready(&polling), Hints: hints}
	var webhookServer *http.Server
	if c.WebhookURL != "" {
		if err := poller.Verify(run); err != nil {
			return err
		}
		hook := bot.NewWebhook(run, poller, c.WebhookSecret)
		// Use exact path matching rather than ServeMux patterns for configured URLs.
		webhookServer = &http.Server{Addr: c.WebhookListen, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != c.WebhookPath {
				http.NotFound(w, r)
				return
			}
			hook.ServeHTTP(w, r)
		}), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return run }}
		workers.Go(hook.Run)
		workers.Go(func() { errorsCh <- webhookServer.ListenAndServe() })
	} else {
		workers.Go(func() { errorsCh <- poller.Run(run) })
	}
	workers.Go(func() {
		errorsCh <- (&delivery.Sender{Backend: backend, Telegram: tg, Limiter: limiter, Brokers: c.Brokers, Prefix: c.Prefix, Group: c.Group, Worker: worker, Outcomes: outcomes, Ready: ready(&sending), Hints: hints}).Run(run)
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
	if webhookServer != nil {
		if webhookServer.Shutdown(shutdown) != nil {
			_ = webhookServer.Close()
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

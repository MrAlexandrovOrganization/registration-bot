package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"registration.local/frontend/internal/app"
	"registration.local/frontend/internal/observability"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	shutdown, err := observability.Init(ctx)
	if err != nil {
		slog.Error("telemetry initialization failed")
		return 1
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()
	c, err := app.Load()
	if err != nil {
		slog.Error("invalid configuration", "reason", err.Error())
		return 1
	}
	if err = app.Run(ctx, c); err != nil {
		slog.Error("frontend stopped", "reason", err.Error())
		return 1
	}
	return 0
}

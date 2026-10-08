package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
)

type senderClaimer interface {
	Claim(context.Context, *pb.ClaimRequest, ...grpc.CallOption) (*pb.Delivery, error)
}

// Keep one identity across retries: an RPC response can be lost after acquisition.
// A new process must use a fresh identity to preserve single-sender exclusion.
func waitForSender(ctx context.Context, backend senderClaimer, worker string) error {
	startup, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	lastCode := codes.OK
	for {
		if startup.Err() != nil {
			return fmt.Errorf("sender startup interrupted: %w (last grpc_code=%s)", startup.Err(), lastCode)
		}
		call, done := context.WithTimeout(startup, 10*time.Second)
		_, err := backend.Claim(call, &pb.ClaimRequest{Worker: worker})
		done()
		if err == nil {
			slog.InfoContext(ctx, "sender lease acquired")
			return nil
		}
		lastCode = status.Code(err)
		if startup.Err() != nil {
			continue
		}
		var reason string
		switch lastCode {
		case codes.ResourceExhausted:
			reason = "sender lease is held by another process; waiting for expiry or release"
		case codes.Unavailable, codes.DeadlineExceeded:
			reason = "backend temporarily unavailable"
		default:
			// Never include backend response descriptions: they may contain sensitive data.
			return fmt.Errorf("sender startup rejected: grpc_code=%s", lastCode)
		}
		slog.WarnContext(ctx, "waiting to acquire sender lease", "reason", reason, "grpc_code", lastCode.String(), "retry_in", "2s")
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-startup.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

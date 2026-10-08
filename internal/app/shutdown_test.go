package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
)

type releaseBackend struct {
	release func(context.Context, *pb.ReleaseSenderRequest) (*pb.ReleaseSenderResponse, error)
}

func (b releaseBackend) ReleaseSender(ctx context.Context, r *pb.ReleaseSenderRequest, _ ...grpc.CallOption) (*pb.ReleaseSenderResponse, error) {
	return b.release(ctx, r)
}

func TestShutdownReleasesOnlyAfterWorkersFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		run, stop := context.WithCancel(ctx)
		var workers sync.WaitGroup
		finished := false
		workers.Go(func() {
			<-run.Done()
			// Model the fresh Complete reporting context after cancellation.
			time.Sleep(3 * time.Second)
			finished = true
		})
		calls := 0
		backend := releaseBackend{release: func(ctx context.Context, r *pb.ReleaseSenderRequest) (*pb.ReleaseSenderResponse, error) {
			calls++
			if !finished || ctx.Err() != nil || r.Worker != "old-worker" {
				t.Fatal("release before worker completion or with cancelled context")
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) != 5*time.Second {
				t.Fatal("release must have its own five-second timeout")
			}
			return &pb.ReleaseSenderResponse{Released: true}, nil
		}}
		cancel()
		stopAndReleaseSender(ctx, stop, &workers, backend, "old-worker")
		if calls != 1 {
			t.Fatalf("release calls = %d", calls)
		}
	})
}

func TestShutdownReleaseFailureIsBoundedAndSanitized(t *testing.T) {
	for _, code := range []codes.Code{codes.Unimplemented, codes.Unavailable, codes.DeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logs bytes.Buffer
				old := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				defer slog.SetDefault(old)
				start := time.Now()
				backend := releaseBackend{release: func(ctx context.Context, _ *pb.ReleaseSenderRequest) (*pb.ReleaseSenderResponse, error) {
					if code == codes.DeadlineExceeded {
						<-ctx.Done()
					}
					return nil, status.Error(code, "synthetic-secret")
				}}
				ctx, stop := context.WithCancel(context.Background())
				var workers sync.WaitGroup
				stopAndReleaseSender(ctx, stop, &workers, backend, "old-worker")
				if ctx.Err() == nil || strings.Contains(logs.String(), "synthetic-secret") || !strings.Contains(logs.String(), code.String()) {
					t.Fatal("shutdown did not stop workers or sanitize failure")
				}
				if elapsed := time.Since(start); elapsed > 5*time.Second || code == codes.DeadlineExceeded && elapsed != 5*time.Second {
					t.Fatalf("release duration: %v", elapsed)
				}
			})
		})
	}
}

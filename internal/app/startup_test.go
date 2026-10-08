package app

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
)

type startupBackend struct {
	claim func(context.Context, *pb.ClaimRequest) (*pb.Delivery, error)
}

func (b startupBackend) Claim(ctx context.Context, r *pb.ClaimRequest, _ ...grpc.CallOption) (*pb.Delivery, error) {
	return b.claim(ctx, r)
}

func TestSenderStartupWaitsForPreviousLease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		backend := startupBackend{claim: func(ctx context.Context, r *pb.ClaimRequest) (*pb.Delivery, error) {
			calls++
			if r.Worker != "new-process" || r.Id != 0 {
				t.Fatalf("unexpected claim: %v", r)
			}
			deadline, ok := ctx.Deadline()
			if !ok || deadline.Sub(time.Now()) > 10*time.Second {
				t.Fatal("claim must have a bounded deadline")
			}
			if time.Since(start) < 90*time.Second {
				return nil, status.Error(codes.ResourceExhausted, "another sender")
			}
			return &pb.Delivery{}, nil
		}}
		if err := waitForSender(context.Background(), backend, "new-process"); err != nil {
			t.Fatal(err)
		}
		if time.Since(start) != 90*time.Second || calls != 46 {
			t.Fatalf("elapsed=%v calls=%d", time.Since(start), calls)
		}
	})
}

func TestSenderStartupErrorPolicy(t *testing.T) {
	for _, code := range []codes.Code{codes.Unavailable, codes.DeadlineExceeded, codes.Unauthenticated, codes.PermissionDenied, codes.InvalidArgument, codes.Internal} {
		t.Run(code.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var logs bytes.Buffer
				old := slog.Default()
				slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
				defer slog.SetDefault(old)
				calls := 0
				backend := startupBackend{claim: func(context.Context, *pb.ClaimRequest) (*pb.Delivery, error) {
					calls++
					if calls == 1 {
						return nil, status.Error(code, "synthetic-secret")
					}
					return &pb.Delivery{}, nil
				}}
				err := waitForSender(context.Background(), backend, "worker")
				retry := code == codes.Unavailable || code == codes.DeadlineExceeded
				if retry && (err != nil || calls != 2) {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				if !retry && (err == nil || calls != 1 || !strings.Contains(err.Error(), code.String())) {
					t.Fatalf("calls=%d err=%v", calls, err)
				}
				if strings.Contains(logs.String(), "synthetic-secret") || err != nil && strings.Contains(err.Error(), "synthetic-secret") {
					t.Fatal("backend description leaked")
				}
			})
		})
	}
}

func TestSenderStartupTimeoutAndCancellation(t *testing.T) {
	for _, cancelAfter := range []time.Duration{time.Second, 0} {
		t.Run(cancelAfter.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if cancelAfter > 0 {
					time.AfterFunc(cancelAfter, cancel)
				}
				start := time.Now()
				backend := startupBackend{claim: func(context.Context, *pb.ClaimRequest) (*pb.Delivery, error) {
					return nil, status.Error(codes.ResourceExhausted, "busy")
				}}
				err := waitForSender(ctx, backend, "worker")
				want := cancelAfter
				if want == 0 {
					want = 120 * time.Second
				}
				if err == nil || time.Since(start) != want {
					t.Fatalf("elapsed=%v err=%v", time.Since(start), err)
				}
			})
		})
	}
}

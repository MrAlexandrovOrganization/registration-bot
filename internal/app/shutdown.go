package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
)

type senderReleaser interface {
	ReleaseSender(context.Context, *pb.ReleaseSenderRequest, ...grpc.CallOption) (*pb.ReleaseSenderResponse, error)
}

func stopAndReleaseSender(ctx context.Context, stop context.CancelFunc, workers *sync.WaitGroup, backend senderReleaser, worker string) {
	stop()
	workers.Wait() // Includes sender heartbeats, Telegram calls and durable Complete.
	release, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, err := backend.ReleaseSender(release, &pb.ReleaseSenderRequest{Worker: worker})
	if err != nil {
		// Old backends may return Unimplemented. TTL remains the recovery path.
		slog.WarnContext(release, "sender lease release failed; expiry will recover ownership", "grpc_code", status.Code(err).String())
		return
	}
	slog.InfoContext(release, "sender lease cleanup completed", "released", result.GetReleased())
}

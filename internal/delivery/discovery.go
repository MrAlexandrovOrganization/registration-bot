package delivery

import (
	"context"
	"time"

	"github.com/segmentio/kafka-go"
	pb "registration.local/frontend/api"
)

func (s *Sender) recoverInteractive(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		receipt, err := s.Backend.PendingInteractive(call, &pb.PendingInteractiveRequest{Limit: 100})
		cancel()
		if err == nil {
			s.Hints.offer(receipt.GetDeliveryIds(), s.Hints.recovery)
			delay = time.Second
		} else {
			delay = min(15*time.Second, delay*2)
		}
		if Sleep(ctx, delay) != nil {
			return
		}
	}
}

type broadcastReader interface {
	FetchMessage(context.Context) (kafka.Message, error)
	CommitMessages(context.Context, ...kafka.Message) error
}

// Fetch and commit live outside the sender, with at most one outstanding record.
func (s *Sender) consumeBroadcast(ctx context.Context, reader broadcastReader, out chan<- fetched) {
	for ctx.Err() == nil {
		message, err := reader.FetchMessage(ctx)
		if err != nil {
			if Sleep(ctx, time.Second) != nil {
				return
			}
			continue
		}
		f := fetched{message: message, ack: make(chan error, 1), bulk: true}
		for ctx.Err() == nil {
			select {
			case out <- f:
			case <-ctx.Done():
				return
			}
			select {
			case err = <-f.ack:
			case <-ctx.Done():
				return
			}
			if err == nil {
				break
			}
			if Sleep(ctx, time.Second) != nil {
				return
			}
		}
		for ctx.Err() == nil {
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = reader.CommitMessages(call, message)
			cancel()
			if err == nil {
				break
			}
			if Sleep(ctx, time.Second) != nil {
				return
			}
		}
	}
}

package delivery

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/resources"
	"registration.local/frontend/internal/telegram"
)

type Sender struct {
	Backend               pb.RegistrationClient
	Telegram              *telegram.Client
	Limiter               *Limiter
	Brokers               []string
	Prefix, Group, Worker string
	Outcomes              *prometheus.CounterVec
	Ready                 func(bool)
}
type fetched struct {
	message kafka.Message
	reader  *kafka.Reader
	ack     chan struct{}
	bulk    bool
}

func (s *Sender) Run(ctx context.Context) error {
	worker := s.Worker
	interactive := make(chan fetched)
	bulk := make(chan fetched)
	run, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	for _, q := range []struct {
		kind string
		out  chan fetched
		bulk bool
	}{{"interactive", interactive, false}, {"broadcast", bulk, true}} {
		workers.Go(func() {
			reader := kafka.NewReader(kafka.ReaderConfig{Brokers: s.Brokers, Topic: s.Prefix + "." + q.kind + ".v1", GroupID: s.Group + "-" + q.kind, MinBytes: 1, MaxBytes: 1 << 20, MaxWait: time.Second, CommitInterval: 0, StartOffset: kafka.FirstOffset, QueueCapacity: 1})
			defer reader.Close()
			for run.Err() == nil {
				message, err := reader.FetchMessage(run)
				if err != nil {
					if run.Err() == nil {
						slog.Warn("Kafka fetch deferred")
						_ = Sleep(run, time.Second)
					}
					continue
				}
				f := fetched{message, reader, make(chan struct{}), q.bulk}
				select {
				case q.out <- f:
				case <-run.Done():
					return
				}
				select {
				case <-f.ack:
				case <-run.Done():
					return
				}
			}
		})
	}
	defer workers.Wait() // cancel before waiting when leaving the function.
	defer cancel()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for ctx.Err() == nil {
		var f fetched
		select {
		case f = <-interactive:
		default:
			select {
			case <-ctx.Done():
				return nil
			case <-heartbeat.C:
				call, cancel := context.WithTimeout(ctx, 5*time.Second)
				_, err := s.Backend.Claim(call, &pb.ClaimRequest{Worker: worker})
				cancel()
				s.Ready(err == nil)
				continue
			case f = <-interactive:
			case f = <-bulk:
			}
		}
		for ctx.Err() == nil {
			err := s.handle(ctx, worker, f)
			if err == nil {
				break
			}
			s.Ready(false)
			slog.Warn("delivery deferred", "code", status.Code(err).String())
			if Sleep(ctx, time.Second) != nil {
				return nil
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		for ctx.Err() == nil {
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := f.reader.CommitMessages(call, f.message)
			cancel()
			if err == nil {
				break
			}
			if Sleep(ctx, time.Second) != nil {
				return nil
			}
		}
		close(f.ack)
	}
	return nil
}
func (s *Sender) handle(ctx context.Context, worker string, f fetched) error {
	id, err := strconv.ParseInt(string(f.message.Value), 10, 64)
	if err != nil || id <= 0 {
		slog.Warn("invalid Kafka message discarded")
		return nil
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	d, err := s.Backend.Claim(call, &pb.ClaimRequest{Id: id, Worker: worker})
	cancel()
	if err != nil {
		return err
	}
	s.Ready(true)
	if delay := time.Until(time.UnixMilli(d.NotBeforeUnixMs)); delay > 0 {
		// No job was claimed. SQL retains it; Kafka can be acknowledged and scheduler republishes after cooldown.
		return nil
	}
	if d.Id == 0 {
		return nil
	}
	parent := otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier{"traceparent": d.Traceparent})
	work, cancel := context.WithTimeout(parent, 55*time.Second)
	defer cancel()
	work, span := otel.Tracer("registration-sender").Start(work, "telegram.delivery")
	defer span.End()
	completion := &pb.Completion{Id: d.Id, Lease: d.Lease}
	if err = s.Limiter.Wait(work, d.Chat, d.Group, f.bulk); err == nil {
		var message int64
		switch d.Kind {
		case "view":
			text, markup := resources.Render(d.View)
			message, err = s.Telegram.Send(work, d.Chat, text, markup)
		case "copy":
			message, err = s.Telegram.Copy(work, d.Chat, d.SourceChat, d.SourceMessage)
		case "export":
			var file *pb.ExportResponse
			file, err = s.Backend.Export(work, &pb.ExportRequest{Actor: d.Actor})
			if err == nil {
				message, err = s.Telegram.Document(work, d.Chat, file.Xlsx)
			}
		case "sync":
			err = s.syncMembers(work, d, completion)
		default:
			err = &telegram.APIError{Code: 400}
		}
		completion.TelegramMessageId = message
	}
	completion.Outcome, completion.RetryAfterSeconds = Classify(err)
	if d.Kind == "sync" && completion.Outcome == "blocked" {
		completion.Outcome = "permanent"
	}
	// Report using a fresh context: cancellation of an HTTP call must not prevent durable completion.
	report, cancel := context.WithTimeout(context.WithoutCancel(work), 10*time.Second)
	defer cancel()
	_, err = s.Backend.Complete(report, completion)
	if err == nil {
		s.Outcomes.WithLabelValues(completion.Outcome).Inc()
		slog.InfoContext(work, "delivery attempt completed", "outcome", completion.Outcome)
	}
	if status.Code(err) == codes.FailedPrecondition {
		return nil
	}
	return err
}
func (s *Sender) syncMembers(ctx context.Context, d *pb.Delivery, out *pb.Completion) error {
	if d.View == nil || len(d.View.Numbers) != 3 {
		return &telegram.APIError{Code: 400}
	}
	after := d.View.Numbers[0]
	out.Checked = d.View.Numbers[1]
	out.FailedMembers = d.View.Numbers[2]
	members, err := s.Backend.Members(ctx, &pb.MembersRequest{Actor: d.Actor, Role: d.View.Code, After: after})
	if err != nil {
		return err
	}
	for _, id := range members.Users {
		if err = Sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
		var member struct {
			Status   string `json:"status"`
			IsMember bool   `json:"is_member"`
		}
		err = s.Telegram.Call(ctx, "getChatMember", struct {
			Chat int64 `json:"chat_id"`
			User int64 `json:"user_id"`
		}{members.Chat, id}, &member)
		if err != nil {
			var api *telegram.APIError
			if errors.As(err, &api) && api.Code == 400 {
				out.FailedMembers++
				continue
			}
			return err
		}
		_, err = s.Backend.SyncMember(ctx, &pb.SyncMemberRequest{Actor: d.Actor, Chat: members.Chat, User: id, Active: telegram.Active(member.Status, member.IsMember)})
		if err != nil {
			return err
		}
		out.Checked++
	}
	if len(members.Users) == 20 {
		out.NextAfter = members.Users[len(members.Users)-1]
	}
	return nil
}
func Classify(err error) (string, int64) {
	if err == nil {
		return "sent", 0
	}
	var api *telegram.APIError
	if errors.As(err, &api) {
		if api.Uncertain {
			return "uncertain", 0
		}
		switch {
		case api.Code == 429:
			return "rate_limit", max(1, api.RetryAfter)
		case api.Code == 403:
			return "blocked", 0
		case api.Code >= 400 && api.Code < 500:
			return "permanent", 0
		default:
			return "transient", 0
		}
	}
	if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.NotFound {
		return "permanent", 0
	}
	return "transient", 0
}

package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/delivery"
	"registration.local/frontend/internal/telegram"
)

type Poller struct {
	Telegram *telegram.Client
	Backend  pb.RegistrationClient
	BotID    int64
	Updates  *prometheus.CounterVec
	Ready    func(bool)
}

func Convert(u telegram.Update) *pb.Update {
	result := &pb.Update{Id: u.ID}
	var from telegram.User
	var chat telegram.Chat
	switch {
	case u.Callback != nil:
		if u.Callback.Message == nil {
			return nil
		}
		from = u.Callback.From
		chat = u.Callback.Message.Chat
		result.Callback = u.Callback.Data
		result.MessageId = u.Callback.Message.ID
	case u.Message != nil:
		from = u.Message.From
		chat = u.Message.Chat
		result.Text = u.Message.Text
		result.MessageId = u.Message.ID
		result.Kind = "message"
		if u.Message.Contact != nil {
			result.Phone = u.Message.Contact.Phone
			result.ContactOwner = u.Message.Contact.Owner
		}
	case u.Membership != nil || u.BotMembership != nil:
		m := u.Membership
		result.Kind = "member"
		if m == nil {
			m = u.BotMembership
			result.Kind = "blocked"
		}
		from = m.From
		chat = m.Chat
		result.MemberId = m.New.User.ID
		result.MemberActive = telegram.Active(m.New.Status, m.New.IsMember)
	default:
		return nil
	}
	result.Actor = from.ID
	result.Chat = chat.ID
	result.ChatType = chat.Type
	result.ChatTitle = chat.Title
	result.Username = from.Username
	result.DisplayName = strings.TrimSpace(from.FirstName + " " + from.LastName)
	if result.Actor <= 0 {
		return nil
	}
	return result
}
func (p *Poller) Run(ctx context.Context) error {
	var me telegram.User
	if err := p.Telegram.Call(ctx, "getMe", struct{}{}, &me); err != nil {
		return errors.New("cannot identify Telegram bot")
	}
	if me.ID != p.BotID {
		return errors.New("Telegram bot identity does not match token prefix")
	}
	var offset int64
	for ctx.Err() == nil {
		var updates []telegram.Update
		err := p.Telegram.Call(ctx, "getUpdates", struct {
			Offset  int64    `json:"offset"`
			Timeout int      `json:"timeout"`
			Limit   int      `json:"limit"`
			Allowed []string `json:"allowed_updates"`
		}{offset, 25, 100, []string{"message", "callback_query", "my_chat_member", "chat_member"}}, &updates)
		if err != nil {
			p.Ready(false)
			slog.Warn("Telegram polling deferred")
			delay := time.Second
			var api *telegram.APIError
			if errors.As(err, &api) && api.RetryAfter > 0 {
				delay = time.Duration(api.RetryAfter) * time.Second
			}
			if delivery.Sleep(ctx, delay) != nil {
				return nil
			}
			continue
		}
		p.Ready(true)
		for _, raw := range updates {
			update := Convert(raw)
			if update != nil {
				work, span := otel.Tracer("registration-telegram").Start(ctx, "telegram.update")
				for ctx.Err() == nil {
					call, cancel := context.WithTimeout(work, 15*time.Second)
					_, err = p.Backend.Accept(call, update)
					cancel()
					if err == nil {
						p.Updates.WithLabelValues("accepted").Inc()
						break
					}
					if status.Code(err) == codes.InvalidArgument {
						p.Updates.WithLabelValues("invalid").Inc()
						break
					}
					p.Ready(false)
					slog.WarnContext(work, "update persistence deferred", "code", status.Code(err).String())
					if delivery.Sleep(ctx, time.Second) != nil {
						span.End()
						return nil
					}
				}
				span.End()
				if raw.Callback != nil {
					// This only dismisses Telegram's spinner after persistence; all content messages use the sender.
					call, cancel := context.WithTimeout(ctx, 3*time.Second)
					_ = p.Telegram.Call(call, "answerCallbackQuery", struct {
						ID string `json:"callback_query_id"`
					}{raw.Callback.ID}, nil)
					cancel()
				}
			}
			if ctx.Err() != nil {
				return nil
			}
			offset = raw.ID + 1
		}
	}
	return nil
}

package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/status"
	pb "registration.local/frontend/api"
	"registration.local/frontend/internal/delivery"
	"registration.local/frontend/internal/resources"
	"registration.local/frontend/internal/telegram"
)

type Poller struct {
	Telegram *telegram.Client
	Backend  pb.RegistrationClient
	BotID    int64
	Updates  *prometheus.CounterVec
	Ready    func(bool)
	Hints    interface{ Offer([]int64) }
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
		// pinChatMessage produces a service update, potentially authored by the
		// bot in a user's private chat. It is not an answer or a new command.
		if u.Message.HasPinnedMessage {
			return nil
		}
		from = u.Message.From
		chat = u.Message.Chat
		result.Text = u.Message.Text
		// Reply buttons arrive as ordinary text messages, without callback_data.
		if chat.Type == "private" && result.Text == resources.Text("cancel") {
			result.Text = "/cancel"
		}
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
func (p *Poller) Verify(ctx context.Context) error {
	var me telegram.User
	if err := p.Telegram.Call(ctx, "getMe", struct{}{}, &me); err != nil {
		return errors.New("cannot identify Telegram bot")
	}
	if me.ID != p.BotID {
		return errors.New("Telegram bot identity does not match token prefix")
	}
	return nil
}
func (p *Poller) Run(ctx context.Context) error {
	if err := p.Verify(ctx); err != nil {
		return err
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
			for ctx.Err() == nil {
				err = p.Accept(ctx, raw)
				if err == nil {
					break
				}
				p.Ready(false)
				slog.WarnContext(ctx, "update persistence deferred", "code", status.Code(err).String())
				if delivery.Sleep(ctx, time.Second) != nil {
					return nil
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

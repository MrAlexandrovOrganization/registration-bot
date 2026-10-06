package bot

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"registration.local/frontend/internal/telegram"
)

// Accept is shared by polling and webhook. Content always goes through Hints.
func (p *Poller) Accept(ctx context.Context, raw telegram.Update) error {
	u := Convert(raw)
	if u == nil {
		return nil
	} // unsupported update has no domain effect
	if raw.Callback != nil {
		m := raw.Callback.Message
		u.CallbackMessageEditable = raw.Callback.InlineMessageID == "" && m != nil &&
			m.Date > 0 && m.ID > 0 && m.Chat.ID == u.Chat && m.From.ID == p.BotID &&
			m.Text != "" && m.Caption == "" && !m.HasMedia
	}
	work, span := otel.Tracer("registration-telegram").Start(ctx, "telegram.update")
	defer span.End()
	call, cancel := context.WithTimeout(work, 15*time.Second)
	receipt, err := p.Backend.Accept(call, u)
	cancel()
	if err != nil {
		return err
	}
	// duplicate=true must replay IDs: the original Accept response may be lost.
	if p.Hints != nil {
		p.Hints.Offer(receipt.GetDeliveryIds())
	}
	p.Updates.WithLabelValues("accepted").Inc()
	p.Ready(true)
	if raw.Callback != nil {
		call, cancel := context.WithTimeout(work, 3*time.Second)
		defer cancel()
		_ = p.Telegram.Call(call, "answerCallbackQuery", struct {
			ID string `json:"callback_query_id"`
		}{raw.Callback.ID}, nil)
	}
	return nil
}

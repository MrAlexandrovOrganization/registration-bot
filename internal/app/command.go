package app

import (
	"context"
	"errors"
	"time"

	"registration.local/frontend/internal/telegram"
)

// Execute keeps webhook registration an explicit operator action, never startup.
func Execute(ctx context.Context, c Config, args []string) error {
	if len(args) == 0 {
		return Run(ctx, c)
	}
	if len(args) != 1 {
		return errors.New("expected register-webhook or delete-webhook")
	}
	return webhookCommand(ctx, telegram.New(c.Token, c.TelegramLocalAPI), c, args[0])
}

func webhookCommand(ctx context.Context, tg *telegram.Client, c Config, command string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	switch command {
	case "register-webhook":
		if c.WebhookURL == "" {
			return errors.New("WEBHOOK_URL required")
		}
		return tg.Call(ctx, "setWebhook", struct {
			URL         string   `json:"url"`
			Secret      string   `json:"secret_token"`
			Allowed     []string `json:"allowed_updates"`
			Connections int      `json:"max_connections"`
			Drop        bool     `json:"drop_pending_updates"`
		}{c.WebhookURL, c.WebhookSecret, []string{"message", "callback_query", "my_chat_member", "chat_member"}, 1, false}, nil)
	case "delete-webhook":
		return tg.Call(ctx, "deleteWebhook", struct {
			Drop bool `json:"drop_pending_updates"`
		}{false}, nil)
	default:
		return errors.New("unknown command: expected register-webhook or delete-webhook")
	}
}

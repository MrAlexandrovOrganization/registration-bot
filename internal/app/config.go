package app

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Token, BackendToken, Backend, CA, HTTP, Prefix, Group string
	BotID                                                 int64
	Brokers                                               []string
	TotalRate, BulkRate                                   int
	Insecure                                              bool
	TelegramLocalAPI                                      string
	WebhookURL, WebhookSecret, WebhookListen, WebhookPath string
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func Load() (Config, error) {
	c := Config{Token: os.Getenv("BOT_TOKEN"), BackendToken: os.Getenv("BACKEND_TOKEN"), Backend: env("BACKEND_ADDR", "registration-backend:50051"), CA: os.Getenv("GRPC_CA_FILE"), HTTP: env("HTTP_LISTEN", ":9091"), Prefix: env("KAFKA_TOPIC_PREFIX", "registration.telegram"), Group: env("KAFKA_GROUP", "registration-telegram"), Brokers: strings.Split(env("KAFKA_BROKERS", "kafka:9092"), ",")}
	var err error
	id, secret, ok := strings.Cut(c.Token, ":")
	c.BotID, err = strconv.ParseInt(id, 10, 64)
	if !ok || secret == "" || err != nil || c.BotID <= 0 || strconv.FormatInt(c.BotID, 10) != id {
		return c, errors.New("invalid BOT_TOKEN format")
	}
	if len(c.BackendToken) < 32 {
		return c, errors.New("BOT_TOKEN and BACKEND_TOKEN (32+ characters) required")
	}
	c.TotalRate, err = strconv.Atoi(env("TOTAL_RATE", "20"))
	if err != nil || c.TotalRate < 1 || c.TotalRate > 25 {
		return c, errors.New("TOTAL_RATE must be 1..25")
	}
	c.BulkRate, err = strconv.Atoi(env("BROADCAST_RATE", "15"))
	if err != nil || c.BulkRate < 1 || c.BulkRate >= c.TotalRate {
		return c, errors.New("BROADCAST_RATE must be positive and below TOTAL_RATE")
	}
	c.Insecure, err = strconv.ParseBool(env("ALLOW_INSECURE_GRPC", "false"))
	if err != nil {
		return c, errors.New("invalid ALLOW_INSECURE_GRPC")
	}
	if !c.Insecure && c.CA == "" {
		return c, errors.New("GRPC_CA_FILE required unless plaintext explicitly enabled")
	}
	c.TelegramLocalAPI, err = telegramOrigin(os.Getenv("TELEGRAM_LOCAL_API_URL"))
	if err != nil {
		return c, err
	}
	c.WebhookURL = os.Getenv("WEBHOOK_URL")
	c.WebhookSecret = os.Getenv("TELEGRAM_WEBHOOK_SECRET")
	c.WebhookListen = env("WEBHOOK_LISTEN_ADDR", ":8080")
	if c.WebhookURL != "" {
		u, err := url.Parse(c.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return c, errors.New("WEBHOOK_URL must be an HTTPS URL without credentials, query or fragment")
		}
		c.WebhookPath = u.Path
		if c.WebhookPath == "" {
			c.WebhookPath = "/"
		}
		if len(c.WebhookSecret) < 1 || len(c.WebhookSecret) > 256 || strings.Trim(c.WebhookSecret, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") != "" {
			return c, errors.New("TELEGRAM_WEBHOOK_SECRET must contain 1..256 URL-safe characters")
		}
	}
	return c, nil
}

func telegramOrigin(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Opaque != "" ||
		(u.Scheme != "http" && u.Scheme != "https") || (u.Path != "" && u.Path != "/") ||
		strings.ContainsAny(raw, "?#%\\") {
		return "", errors.New("invalid TELEGRAM_LOCAL_API_URL origin")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return "", errors.New("invalid TELEGRAM_LOCAL_API_URL port")
		}
	}
	return strings.TrimRight(raw, "/"), nil
}

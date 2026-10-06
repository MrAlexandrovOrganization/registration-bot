package bot

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"registration.local/frontend/internal/telegram"
)

type webhookJob struct {
	ctx    context.Context
	update telegram.Update
	done   chan error
}

type Webhook struct {
	poller *Poller
	secret [32]byte
	queue  chan webhookJob
	slots  chan struct{}
	ctx    context.Context
}

func NewWebhook(ctx context.Context, p *Poller, secret string) *Webhook {
	return &Webhook{poller: p, secret: sha256.Sum256([]byte(secret)), queue: make(chan webhookJob, 100), slots: make(chan struct{}, 101), ctx: ctx}
}

// One consumer preserves admission order; the backend owns duplicate/state fencing.
// Queue cancellation never closes a channel underneath an HTTP handler.
func (h *Webhook) Run() {
	for h.ctx.Err() == nil {
		select {
		case <-h.ctx.Done():
			return
		case job := <-h.queue:
			ctx, cancel := context.WithCancel(job.ctx)
			stop := context.AfterFunc(h.ctx, cancel)
			err := ctx.Err()
			if err == nil {
				err = h.poller.Accept(ctx, job.update)
			}
			stop()
			cancel()
			job.done <- err
		}
	}
}

func (h *Webhook) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provided := sha256.Sum256([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")))
	if subtle.ConstantTimeCompare(provided[:], h.secret[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if h.ctx.Err() != nil {
		http.Error(w, "stopping", http.StatusServiceUnavailable)
		return
	}
	// Bound allocations and body readers as well as the decoded update queue.
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var update telegram.Update
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&update)
	if err == nil {
		err = decoder.Decode(&struct{}{})
		if err == io.EOF {
			err = nil
		} else if err == nil {
			err = errors.New("trailing JSON")
		}
	}
	if err != nil || update.ID <= 0 {
		code := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			code = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid update", code)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	job := webhookJob{ctx: ctx, update: update, done: make(chan error, 1)}
	select {
	case h.queue <- job:
	default:
		http.Error(w, "busy", http.StatusServiceUnavailable)
		return
	}
	select {
	case err := <-job.done:
		if err == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
	case <-ctx.Done():
	case <-h.ctx.Done():
	}
	http.Error(w, "persistence unavailable", http.StatusServiceUnavailable)
}

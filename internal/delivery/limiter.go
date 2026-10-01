package delivery

import (
	"context"
	"sync"
	"time"
)

// Limiter is shared by every sender path for one bot. There is no accumulated burst credit.
type Limiter struct {
	mu                          sync.Mutex
	next, nextBulk              time.Time
	chats                       map[int64]time.Time
	totalInterval, bulkInterval time.Duration
}

func NewLimiter(total, bulk int) *Limiter {
	return &Limiter{chats: map[int64]time.Time{}, totalInterval: time.Second / time.Duration(total), bulkInterval: time.Second / time.Duration(bulk)}
}
func (l *Limiter) Wait(ctx context.Context, chat int64, group, bulk bool) error {
	l.mu.Lock()
	now := time.Now()
	at := now
	if l.next.After(at) {
		at = l.next
	}
	if bulk && l.nextBulk.After(at) {
		at = l.nextBulk
	}
	if l.chats[chat].After(at) {
		at = l.chats[chat]
	}
	l.next = at.Add(l.totalInterval)
	if bulk {
		l.nextBulk = at.Add(l.bulkInterval)
	}
	interval := time.Second
	if group {
		interval = 3 * time.Second
	}
	l.chats[chat] = at.Add(interval)
	// Remove expired entries rather than retain every recipient forever.
	if len(l.chats) > 1024 {
		for id, t := range l.chats {
			if t.Before(now) {
				delete(l.chats, id)
			}
		}
	}
	l.mu.Unlock()
	return Sleep(ctx, time.Until(at))
}
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

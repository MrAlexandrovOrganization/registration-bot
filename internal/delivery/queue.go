package delivery

import "sync"

// Hints is bounded, deduplicates queued/in-flight IDs, and never blocks Accept.
// A dropped hint is safe: PendingInteractive rediscovers the durable job.
type Hints struct {
	mu       sync.Mutex
	ids      map[int64]bool
	queue    chan int64
	recovery chan int64
}

func NewHints() *Hints {
	return &Hints{ids: make(map[int64]bool), queue: make(chan int64, 100), recovery: make(chan int64, 100)}
}
func (h *Hints) Offer(ids []int64) {
	h.offer(ids, h.queue)
}
func (h *Hints) offer(ids []int64, queue chan int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range ids {
		if id <= 0 || h.ids[id] {
			continue
		}
		select {
		case queue <- id:
			h.ids[id] = true
		default:
			return
		}
	}
}
func (h *Hints) done(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ids, id)
}

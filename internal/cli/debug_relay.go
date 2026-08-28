package cli

import "sync"

// debugRelay keeps debug sections ordered without blocking the Bubble Tea
// Update goroutine. Its sender may block until Bubble Tea accepts a message,
// so each send runs after the preceding send in a short goroutine chain.
type debugRelay struct {
	mu   sync.Mutex
	tail <-chan struct{}
	send func(string)
}

func newDebugRelay(send func(string)) *debugRelay {
	ready := make(chan struct{})
	close(ready)
	return &debugRelay{tail: ready, send: send}
}

func (r *debugRelay) enqueue(section string) {
	r.mu.Lock()
	previous := r.tail
	done := make(chan struct{})
	r.tail = done
	r.mu.Unlock()

	go func() {
		defer close(done)
		<-previous
		r.send(section)
	}()
}

func (r *debugRelay) wait() {
	r.mu.Lock()
	tail := r.tail
	r.mu.Unlock()
	<-tail
}

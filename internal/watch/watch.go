// Package watch owns one invocation's bounded output source. It never calls a
// provider or mutates conversation history; the CLI consumes complete batches.
package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"sync"
	"time"
)

const BufferLimit = 32 << 10

type Event struct {
	Source       string `json:"source"`
	Sequence     uint64 `json:"sequence"`
	Output       string `json:"output"`
	OmittedBytes int64  `json:"omitted_bytes"`
	Finished     bool   `json:"finished"`
	Result       string `json:"result,omitempty"`
}

type Status struct {
	Paused        bool      `json:"paused"`
	Source        string    `json:"source"`
	State         string    `json:"state"`
	LastOutput    time.Time `json:"last_output"`
	Batches       uint64    `json:"batches"`
	BufferedBytes int       `json:"buffered_bytes"`
	OmittedBytes  int64     `json:"omitted_bytes"`
}

type Manager struct {
	paused    bool
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	mu        sync.Mutex
	source    string
	state     string
	pattern   *regexp.Regexp
	buf       []byte
	matchTail []byte
	omitted   int64
	matched   bool
	finished  bool
	result    string
	last      time.Time
	sequence  uint64
	cooldown  time.Duration
	delivered time.Time
}

type contextKey struct{}

func WithManager(ctx context.Context, m *Manager) context.Context {
	return context.WithValue(ctx, contextKey{}, m)
}
func FromContext(ctx context.Context) *Manager { m, _ := ctx.Value(contextKey{}).(*Manager); return m }

func New(ctx context.Context, pattern string, cooldown time.Duration) (*Manager, error) {
	r, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("watch pattern: %w", err)
	}
	if cooldown <= 0 {
		return nil, fmt.Errorf("watch cooldown must be positive")
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{ctx: ctx, cancel: cancel, pattern: r, state: "idle", cooldown: cooldown}, nil
}

// Start transfers ownership of run to the invocation. Only one source can run
// at once; a finished batch must be consumed before a replacement starts.
func (m *Manager) Start(source string, run func(context.Context, io.Writer) string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return m.ctx.Err()
	}
	if m.state != "idle" && m.state != "finished" {
		return fmt.Errorf("watch source already active or final event not consumed")
	}
	m.source, m.state = source, "running"
	m.delivered = time.Now()
	m.finished, m.result = false, ""
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		result := run(m.ctx, m)
		m.mu.Lock()
		m.finished, m.result = true, result
		if m.state != "stopped" {
			m.state = "draining"
		}
		m.mu.Unlock()
	}()
	return nil
}

func (m *Manager) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	m.last = time.Now()
	// Match bounded overlapping chunks, including patterns spanning writes.
	for len(p) > 0 {
		take := min(len(p), BufferLimit)
		block := p[:take]
		p = p[take:]
		candidate := append(m.matchTail, block...)
		if m.pattern.Match(candidate) {
			m.matched = true
		}
		m.matchTail = append([]byte(nil), candidate[max(0, len(candidate)-BufferLimit):]...)
		m.buf = append(m.buf, block...)
		if excess := len(m.buf) - BufferLimit; excess > 0 {
			m.omitted += int64(excess)
			m.buf = append([]byte(nil), m.buf[excess:]...)
		}
	}
	return n, nil
}

// Next delivers each range once. A completion always triggers, even if its
// output does not match. Silence never means success.
func (m *Manager) Next(ctx context.Context) (Event, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		if m.state == "stopped" || m.state == "finished" || m.state == "idle" {
			m.mu.Unlock()
			return Event{}, io.EOF
		}
		if !m.paused && (m.finished || (m.matched && time.Since(m.delivered) >= m.cooldown)) {
			m.sequence++
			event := Event{Source: m.source, Sequence: m.sequence, Output: string(m.buf), OmittedBytes: m.omitted, Finished: m.finished, Result: m.result}
			m.buf = nil
			m.matchTail = nil
			m.omitted = 0
			m.matched = false
			m.delivered = time.Now()
			if m.finished {
				m.state = "finished"
			}
			m.mu.Unlock()
			return event, nil
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-m.ctx.Done():
			return Event{}, m.ctx.Err()
		case <-ticker.C:
		}
	}
}
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Status{Paused: m.paused, Source: m.source, State: m.state, LastOutput: m.last, Batches: m.sequence, BufferedBytes: len(m.buf), OmittedBytes: m.omitted}
}
func (m *Manager) Stop()  { m.mu.Lock(); m.state = "stopped"; m.mu.Unlock(); m.cancel() }
func (m *Manager) Close() { m.Stop(); m.wg.Wait() }

// StartFile follows only the opened regular file, starting at EOF. Rotation or
// truncation stops the source instead of silently trusting a replacement path.
func (m *Manager) StartFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("watch requires an existing regular file")
	}
	f, err := openWatchFile(path)
	if err != nil {
		return err
	}
	opened, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		f.Close()
		return fmt.Errorf("watch file changed while opening")
	}
	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		f.Close()
		return err
	}
	err = m.Start(path, func(ctx context.Context, w io.Writer) string {
		defer f.Close()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		buf := make([]byte, 8192)
		for {
			select {
			case <-ctx.Done():
				return "cancelled"
			case <-ticker.C:
			}
			current, err := os.Stat(path)
			if err != nil {
				return fmt.Sprintf("source unavailable: %v", err)
			}
			if !os.SameFile(opened, current) {
				return "source replaced or rotated; monitoring stopped"
			}
			if current.Size() < offset {
				return "source truncated; monitoring stopped"
			}
			// Bound each polling pass so a continuously growing file cannot starve cancellation.
			remaining := current.Size() - offset
			for remaining > 0 {
				if ctx.Err() != nil {
					return "cancelled"
				}
				n, err := f.Read(buf[:min(int64(len(buf)), remaining)])
				if n > 0 {
					_, _ = w.Write(buf[:n])
					offset += int64(n)
					remaining -= int64(n)
				}
				if err != nil && err != io.EOF {
					return fmt.Sprintf("read failed: %v", err)
				}
				if n == 0 {
					break
				}
			}
		}
	})
	if err != nil {
		f.Close()
	}
	return err
}

// Pause gates delivery, not bounded collection.
func (m *Manager) Pause(paused bool) { m.mu.Lock(); defer m.mu.Unlock(); m.paused = paused }

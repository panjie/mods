package app

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
)

// ErrModelIdleTimeout indicates that a provider stream produced no event for
// the configured idle interval. It is distinct from user cancellation so the
// request can be retried safely.
var ErrModelIdleTimeout = errors.New("model stream idle timeout")

type streamEventKind int

const (
	streamEventChunk streamEventKind = iota
	streamEventToolCallsStart
	streamEventToolCalls
	streamEventDone
	streamEventError
)

type streamEventMsg struct {
	kind    streamEventKind
	chunk   proto.Chunk
	results []proto.ToolCallStatus
	runner  *streamRunner
	err     error
}

// streamRunner owns the per-request lifecycle of a provider stream together
// with any tool registry created for that request and the cancel func of the
// stream's derived context. close() is idempotent so the natural completion
// path (receiveCmd returning streamEventDone/streamEventError) and the user
// quit path can both invoke it without double-close issues.
type streamRunner struct {
	stream      stream.Stream
	cleanup     *toolregistry.Registry
	errh        func(error) tea.Msg
	cancel      context.CancelFunc
	cause       func() error
	cancelCause context.CancelCauseFunc
	idleTimeout time.Duration
	closed      atomic.Bool
	usageTaken  atomic.Bool
}

func newStreamRunner(st stream.Stream, cleanup *toolregistry.Registry, cancel context.CancelFunc, errh func(error) tea.Msg) *streamRunner {
	return &streamRunner{stream: st, cleanup: cleanup, cancel: cancel, errh: errh}
}

func (r *streamRunner) withIdleTimeout(ctx context.Context, cancel context.CancelCauseFunc, timeout time.Duration) *streamRunner {
	if timeout <= 0 {
		return r
	}
	r.cause = func() error { return context.Cause(ctx) }
	r.cancelCause = cancel
	r.idleTimeout = timeout
	return r
}

func (r *streamRunner) receiveCmd() tea.Cmd {
	return func() tea.Msg {
		var timer *time.Timer
		var idleFinished atomic.Bool
		if r.idleTimeout > 0 && r.cancelCause != nil {
			timer = time.AfterFunc(r.idleTimeout, func() {
				if idleFinished.CompareAndSwap(false, true) {
					r.cancelCause(fmt.Errorf("%w after %s", ErrModelIdleTimeout, r.idleTimeout))
				}
			})
		}
		if timer != nil {
			defer func() {
				idleFinished.Store(true)
				timer.Stop()
			}()
		}
		if r.stream.Next() {
			chunk, err := r.stream.Current()
			if err != nil && !errors.Is(err, stream.ErrNoContent) {
				r.close()
				return streamEventMsg{kind: streamEventError, runner: r, err: err}
			}
			return streamEventMsg{kind: streamEventChunk, runner: r, chunk: chunk}
		}

		if cause := r.requestCause(); errors.Is(cause, ErrModelIdleTimeout) {
			r.close()
			return streamEventMsg{kind: streamEventError, runner: r, err: cause}
		}
		if err := r.stream.Err(); err != nil {
			r.close()
			return streamEventMsg{kind: streamEventError, runner: r, err: err}
		}

		_ = r.stream.Close()
		return streamEventMsg{kind: streamEventToolCallsStart, runner: r}
	}
}

func (r *streamRunner) requestCause() error {
	if r.cause == nil {
		return nil
	}
	return r.cause()
}

func (r *streamRunner) toolCallsCmd() tea.Cmd {
	return func() tea.Msg {
		return streamEventMsg{
			kind:    streamEventToolCalls,
			runner:  r,
			results: r.stream.CallTools(),
		}
	}
}

func (r *streamRunner) doneMsg() streamEventMsg {
	return streamEventMsg{kind: streamEventDone, runner: r}
}

func (r *streamRunner) messages() []proto.Message {
	return r.stream.Messages()
}

func (r *streamRunner) takeUsage() proto.TokenUsage {
	if r == nil || r.stream == nil {
		return proto.TokenUsage{}
	}
	if !r.usageTaken.CompareAndSwap(false, true) {
		return proto.TokenUsage{}
	}
	return r.stream.Usage()
}

// close releases the stream's context, the underlying HTTP/SSE body, and any
// tool registry created for this request. It is safe to call from multiple
// goroutines (quit path versus the natural receiveCmd error/done path); the
// first caller wins, subsequent calls are no-ops.
func (r *streamRunner) close() {
	if r == nil {
		return
	}
	if !r.closed.CompareAndSwap(false, true) {
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	if r.stream != nil {
		_ = r.stream.Close()
	}
	if r.cleanup != nil {
		_ = r.cleanup.Close()
		r.cleanup = nil
	}
}

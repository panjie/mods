package app

import (
	"context"
	"errors"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
)

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
	stream     stream.Stream
	cleanup    *toolregistry.Registry
	errh       func(error) tea.Msg
	cancel     context.CancelFunc
	closed     atomic.Bool
	usageTaken atomic.Bool
}

func newStreamRunner(st stream.Stream, cleanup *toolregistry.Registry, cancel context.CancelFunc, errh func(error) tea.Msg) *streamRunner {
	return &streamRunner{stream: st, cleanup: cleanup, cancel: cancel, errh: errh}
}

func (r *streamRunner) receiveCmd() tea.Cmd {
	return func() tea.Msg {
		if r.stream.Next() {
			chunk, err := r.stream.Current()
			if err != nil && !errors.Is(err, stream.ErrNoContent) {
				r.close()
				return streamEventMsg{kind: streamEventError, runner: r, err: err}
			}
			return streamEventMsg{kind: streamEventChunk, runner: r, chunk: chunk}
		}

		if err := r.stream.Err(); err != nil {
			r.close()
			return streamEventMsg{kind: streamEventError, runner: r, err: err}
		}

		_ = r.stream.Close()
		return streamEventMsg{kind: streamEventToolCallsStart, runner: r}
	}
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

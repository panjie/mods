package app

import (
	"errors"

	tea "github.com/charmbracelet/bubbletea"
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

type streamRunner struct {
	stream  stream.Stream
	cleanup *toolregistry.Registry
	errh    func(error) tea.Msg
}

func newStreamRunner(st stream.Stream, cleanup *toolregistry.Registry, errh func(error) tea.Msg) *streamRunner {
	return &streamRunner{stream: st, cleanup: cleanup, errh: errh}
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

func (r *streamRunner) close() {
	if r == nil {
		return
	}
	if r.stream != nil {
		_ = r.stream.Close()
	}
	if r.cleanup != nil {
		_ = r.cleanup.Close()
		r.cleanup = nil
	}
}

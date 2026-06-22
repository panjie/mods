package app

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

type scriptedStream struct {
	chunks   []proto.Chunk
	idx      int
	msgs     []proto.Message
	tools    []proto.ToolCallStatus
	err      error
	closed   bool
	toolRuns int
}

func (s *scriptedStream) Next() bool { return s.idx < len(s.chunks) }

func (s *scriptedStream) Current() (proto.Chunk, error) {
	chunk := s.chunks[s.idx]
	s.idx++
	return chunk, nil
}

func (s *scriptedStream) Close() error {
	s.closed = true
	return nil
}

func (s *scriptedStream) Err() error { return s.err }

func (s *scriptedStream) Messages() []proto.Message { return s.msgs }

func (s *scriptedStream) CallTools() []proto.ToolCallStatus {
	s.toolRuns++
	return s.tools
}

func TestStreamRunnerEvents(t *testing.T) {
	errh := func(err error) tea.Msg { return modsError{Err: err} }

	t.Run("chunk then tool start", func(t *testing.T) {
		st := &scriptedStream{chunks: []proto.Chunk{{Content: "hello"}}}
		runner := newStreamRunner(st, nil, errh)

		msg := runner.receiveCmd()().(streamEventMsg)
		require.Equal(t, streamEventChunk, msg.kind)
		require.Equal(t, "hello", msg.chunk.Content)

		msg = runner.receiveCmd()().(streamEventMsg)
		require.Equal(t, streamEventToolCallsStart, msg.kind)
		require.True(t, st.closed)
	})

	t.Run("tool call event", func(t *testing.T) {
		st := &scriptedStream{tools: []proto.ToolCallStatus{{Name: "fs_read_file"}}}
		runner := newStreamRunner(st, nil, errh)

		msg := runner.toolCallsCmd()().(streamEventMsg)
		require.Equal(t, streamEventToolCalls, msg.kind)
		require.Equal(t, "fs_read_file", msg.results[0].Name)
		require.Equal(t, 1, st.toolRuns)
	})

	t.Run("stream error event closes", func(t *testing.T) {
		errBoom := errors.New("boom")
		st := &scriptedStream{err: errBoom}
		runner := newStreamRunner(st, nil, errh)

		msg := runner.receiveCmd()().(streamEventMsg)
		require.Equal(t, streamEventError, msg.kind)
		require.ErrorIs(t, msg.err, errBoom)
		require.True(t, st.closed)
	})
}

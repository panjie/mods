package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

type scriptedStream struct {
	chunks   []proto.Chunk
	idx      int
	msgs     []proto.Message
	tools    []proto.ToolCallStatus
	usage    proto.TokenUsage
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
func (s *scriptedStream) Usage() proto.TokenUsage   { return s.usage }

func (s *scriptedStream) CallTools() []proto.ToolCallStatus {
	s.toolRuns++
	return s.tools
}

func TestStreamRunnerEvents(t *testing.T) {
	errh := func(err error) tea.Msg { return modsError{Err: err} }

	t.Run("chunk then tool start", func(t *testing.T) {
		st := &scriptedStream{chunks: []proto.Chunk{{Content: "hello"}}}
		runner := newStreamRunner(st, nil, nil, errh)

		msg := runner.receiveCmd()().(streamEventMsg)
		require.Equal(t, streamEventChunk, msg.kind)
		require.Equal(t, "hello", msg.chunk.Content)

		msg = runner.receiveCmd()().(streamEventMsg)
		require.Equal(t, streamEventToolCallsStart, msg.kind)
		require.True(t, st.closed)
	})

	t.Run("tool call event", func(t *testing.T) {
		st := &scriptedStream{tools: []proto.ToolCallStatus{{Name: "fs_read_file"}}}
		runner := newStreamRunner(st, nil, nil, errh)

		msg := runner.toolCallsCmd()().(streamEventMsg)
		require.Equal(t, streamEventToolCalls, msg.kind)
		require.Equal(t, "fs_read_file", msg.results[0].Name)
		require.Equal(t, 1, st.toolRuns)
	})

	t.Run("stream error event closes", func(t *testing.T) {
		errBoom := errors.New("boom")
		st := &scriptedStream{err: errBoom}
		runner := newStreamRunner(st, nil, nil, errh)

		msg := runner.receiveCmd()().(streamEventMsg)
		require.Equal(t, streamEventError, msg.kind)
		require.ErrorIs(t, msg.err, errBoom)
		require.True(t, st.closed)
	})
}

// TestStreamRunnerCloseIsIdempotent guards against double-close panics when
// the natural completion path (receiveCmd error branch) and the quit path
// both invoke close() on the same runner.
func TestStreamRunnerCloseIsIdempotent(t *testing.T) {
	closes := atomic.Int32{}
	st := &scriptedStream{}
	st.closed = false
	wrapped := &countingStream{inner: st, closes: &closes}

	cancelCalls := atomic.Int32{}
	cancel := func() { cancelCalls.Add(1) }

	runner := newStreamRunner(wrapped, nil, cancel, func(err error) tea.Msg { return nil })

	runner.close()
	runner.close()
	runner.close()

	require.Equal(t, int32(1), closes.Load(), "underlying stream.Close must be invoked exactly once")
	require.Equal(t, int32(1), cancelCalls.Load(), "cancel must be invoked exactly once")
}

// TestStreamRunnerCloseConcurrent stresses the idempotent close from
// multiple goroutines so a regression would be caught by go test -race.
func TestStreamRunnerCloseConcurrent(t *testing.T) {
	closes := atomic.Int32{}
	st := &scriptedStream{}
	wrapped := &countingStream{inner: st, closes: &closes}

	cancelCalls := atomic.Int32{}
	cancel := func() { cancelCalls.Add(1) }

	runner := newStreamRunner(wrapped, nil, cancel, func(err error) tea.Msg { return nil })

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runner.close()
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), closes.Load())
	require.Equal(t, int32(1), cancelCalls.Load())
}

// TestQuitCancelsActiveRunner verifies m.quit() closes the registered active
// runner (cancelling its derived context, closing the stream body) rather
// than waiting for the provider goroutine to finish on its own.
func TestQuitCancelsActiveRunner(t *testing.T) {
	closes := atomic.Int32{}
	cancelCalls := atomic.Int32{}
	st := &scriptedStream{}
	wrapped := &countingStream{inner: st, closes: &closes}
	cancel := func() { cancelCalls.Add(1) }
	runner := newStreamRunner(wrapped, nil, cancel, func(err error) tea.Msg { return nil })

	m := testMods(t)
	m.setActiveRunner(runner)

	_ = m.quit()

	require.Equal(t, int32(1), closes.Load(), "stream must be closed by quit")
	require.Equal(t, int32(1), cancelCalls.Load(), "stream ctx must be cancelled by quit")
	require.Nil(t, m.takeActiveRunner(), "activeRunner must be cleared after quit")
}

// TestQuitWithoutActiveRunnerNoPanic locks in that quit() is safe when no
// stream is in flight (idle state, very-early quit, etc.).
func TestQuitWithoutActiveRunnerNoPanic(t *testing.T) {
	m := testMods(t)
	require.NotPanics(t, func() { _ = m.quit() })
}

// TestStartCompletionCmdCancelsPriorWork asserts that initiating a new
// completion cancels any in-flight stream from the previous request and
// drains the leftover cancel-func slice (e.g. tool call contexts).
func TestStartCompletionCmdCancelsPriorWork(t *testing.T) {
	closes := atomic.Int32{}
	cancelCalls := atomic.Int32{}
	st := &scriptedStream{}
	wrapped := &countingStream{inner: st, closes: &closes}
	streamCancel := func() { cancelCalls.Add(1) }
	prior := newStreamRunner(wrapped, nil, streamCancel, func(err error) tea.Msg { return nil })

	extraCancelled := atomic.Int32{}
	extraCancel := func() { extraCancelled.Add(1) }

	m := testMods(t)
	m.ctx = context.Background()
	m.setActiveRunner(prior)
	m.addCancel(extraCancel)

	// We don't actually want to run buildRequestSession here; just exercise
	// the synchronous teardown path that startCompletionCmd performs before
	// it returns the tea.Cmd closure. Invoke it for its side effects.
	_ = m.startCompletionCmd("ignored")

	require.Equal(t, int32(1), closes.Load(), "prior stream must be closed")
	require.Equal(t, int32(1), cancelCalls.Load(), "prior stream cancel must fire")
	require.Equal(t, int32(1), extraCancelled.Load(), "tool-call cancel must fire")
	require.Nil(t, m.takeActiveRunner(), "activeRunner must be cleared")
}

// countingStream wraps a stream.Stream and counts Close() invocations so
// idempotency tests can assert "called exactly once".
type countingStream struct {
	inner  *scriptedStream
	closes *atomic.Int32
}

func (s *countingStream) Next() bool                        { return s.inner.Next() }
func (s *countingStream) Current() (proto.Chunk, error)     { return s.inner.Current() }
func (s *countingStream) Err() error                        { return s.inner.Err() }
func (s *countingStream) Messages() []proto.Message         { return s.inner.Messages() }
func (s *countingStream) Usage() proto.TokenUsage           { return s.inner.Usage() }
func (s *countingStream) CallTools() []proto.ToolCallStatus { return s.inner.CallTools() }
func (s *countingStream) Close() error {
	s.closes.Add(1)
	return s.inner.Close()
}

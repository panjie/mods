package watch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestManager(t *testing.T, pattern string) *Manager {
	t.Helper()
	m, err := New(context.Background(), pattern, time.Millisecond)
	require.NoError(t, err)
	t.Cleanup(m.Close)
	return m
}
func nextEvent(t *testing.T, m *Manager) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	e, err := m.Next(ctx)
	require.NoError(t, err)
	return e
}
func TestBoundedCaptureMatchesAcrossWritesAndReportsLoss(t *testing.T) {
	m := newTestManager(t, "ERROR")
	ready := make(chan struct{})
	require.NoError(t, m.Start("source", func(ctx context.Context, w io.Writer) string {
		_, _ = io.WriteString(w, "ER")
		_, _ = io.WriteString(w, "ROR")
		_, _ = io.WriteString(w, strings.Repeat("x", BufferLimit*3))
		close(ready)
		<-ctx.Done()
		return "cancelled"
	}))
	<-ready
	e := nextEvent(t, m)
	require.Equal(t, BufferLimit, len(e.Output))
	require.Equal(t, int64(BufferLimit*2+5), e.OmittedBytes)
	require.Equal(t, uint64(1), e.Sequence)
	require.False(t, e.Finished)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := m.Next(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}
func TestCompletionAlwaysDeliveredAndSourceCanRestart(t *testing.T) {
	m := newTestManager(t, "ERROR")
	require.NoError(t, m.Start("one", func(_ context.Context, w io.Writer) string { _, _ = io.WriteString(w, "ok"); return "exit 0" }))
	e := nextEvent(t, m)
	require.True(t, e.Finished)
	require.Equal(t, "exit 0", e.Result)
	_, err := m.Next(context.Background())
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, m.Start("two", func(_ context.Context, w io.Writer) string { _, _ = io.WriteString(w, "again"); return "exit 1" }))
	e = nextEvent(t, m)
	require.Equal(t, uint64(2), e.Sequence)
	require.Equal(t, "again", e.Output)
}
func TestSingleSourceAndCancellationJoinsWorker(t *testing.T) {
	m := newTestManager(t, "")
	done := make(chan struct{})
	require.NoError(t, m.Start("one", func(ctx context.Context, _ io.Writer) string { defer close(done); <-ctx.Done(); return "cancelled" }))
	require.Error(t, m.Start("two", func(context.Context, io.Writer) string { t.Error("must not run"); return "" }))
	m.Close()
	select {
	case <-done:
	default:
		t.Fatal("worker not joined")
	}
	require.Error(t, m.Start("three", nil))
}
func TestFileStartsAtEOFAndStopsOnTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	require.NoError(t, os.WriteFile(path, []byte("old output\n"), 0600))
	m := newTestManager(t, "ERROR")
	require.NoError(t, m.StartFile(path))
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	require.NoError(t, err)
	_, err = f.WriteString("ERROR new\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	e := nextEvent(t, m)
	require.Equal(t, "ERROR new\n", e.Output)
	require.NoError(t, os.Truncate(path, 0))
	e = nextEvent(t, m)
	require.True(t, e.Finished)
	require.Contains(t, e.Result, "truncated")
}
func TestFileReplacementDoesNotReadNewTarget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0600))
	m := newTestManager(t, "")
	require.NoError(t, m.StartFile(path))
	require.NoError(t, os.Rename(path, path+".old"))
	require.NoError(t, os.WriteFile(path, []byte("replacement contents"), 0600))
	e := nextEvent(t, m)
	require.True(t, e.Finished)
	require.Contains(t, e.Result, "replaced")
	require.Empty(t, e.Output)
}

func TestPauseKeepsCollectingButDefersDelivery(t *testing.T) {
	m := newTestManager(t, "")
	m.Pause(true)
	require.NoError(t, m.Start("source", func(_ context.Context, w io.Writer) string { _, _ = io.WriteString(w, "while paused"); return "done" }))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := m.Next(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Positive(t, m.Status().BufferedBytes)
	m.Pause(false)
	e := nextEvent(t, m)
	require.Equal(t, "while paused", e.Output)
	require.True(t, e.Finished)
}

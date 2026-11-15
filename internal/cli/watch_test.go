package cli

import (
	"context"
	"io"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/watch"
	"github.com/stretchr/testify/require"
)

func TestWatchSerialTurnsPreserveSessionAndStop(t *testing.T) {
	withChatTest(t, "", func(_ *[]string) {
		config.Chat = false
		config.Watch = "process"
		config.WatchMaxRequests = 100
		config.WatchMaxTurns = 3
		config.WatchTimeout = "5s"
		turns := 0
		chatTurn = func(ctx context.Context, prompt string, _ []tea.ProgramOption) (*Mods, error) {
			turns++
			m := watch.FromContext(ctx)
			require.NotNil(t, m)
			if turns == 1 {
				require.NoError(t, m.Start("test", func(_ context.Context, w io.Writer) string { _, _ = io.WriteString(w, "failed"); return "exit 1" }))
			} else {
				require.Equal(t, "watch-session", turnConfig(ctx).Continue)
				require.Contains(t, prompt, "failed")
				require.Contains(t, prompt, "untrusted")
				m.Stop()
			}
			turnConfig(ctx).SessionWriteToID = "watch-session"
			return &Mods{Config: turnConfig(ctx)}, nil
		}
		require.NoError(t, runWatch(context.Background(), []string{"repair the test"}, nil))
		require.Equal(t, 2, turns)
	})
}
func TestWatchTurnLimitCancelsOwnedWork(t *testing.T) {
	withChatTest(t, "", func(_ *[]string) {
		config.Chat = false
		config.Watch = "process"
		config.WatchMaxRequests = 100
		config.WatchMaxTurns = 1
		config.WatchTimeout = "5s"
		done := make(chan struct{})
		chatTurn = func(ctx context.Context, _ string, _ []tea.ProgramOption) (*Mods, error) {
			require.NoError(t, watch.FromContext(ctx).Start("test", func(ctx context.Context, _ io.Writer) string { defer close(done); <-ctx.Done(); return "cancelled" }))
			return &Mods{Config: turnConfig(ctx)}, nil
		}
		require.NoError(t, runWatch(context.Background(), []string{"observe"}, nil))
		select {
		case <-done:
		default:
			t.Fatal("owned work still running")
		}
	})
}
func TestWatchExplicitTurnCancellationStopsInvocation(t *testing.T) {
	withChatTest(t, "", func(_ *[]string) {
		config.Chat = false
		config.Watch = "process"
		config.WatchMaxRequests = 100
		config.WatchMaxTurns = 10
		config.WatchTimeout = "5s"
		chatTurn = func(ctx context.Context, _ string, _ []tea.ProgramOption) (*Mods, error) {
			return &Mods{Config: turnConfig(ctx), TurnCanceled: true}, nil
		}
		require.ErrorIs(t, runWatch(context.Background(), []string{"observe"}, nil), context.Canceled)
	})
}
func TestWatchRejectsInvalidModes(t *testing.T) {
	withChatTest(t, "", func(_ *[]string) {
		config.Chat = false
		config.Watch = "process"
		config.WatchMaxRequests = 100
		config.WatchMaxTurns = 10
		config.WatchTimeout = "5s"
		_, err := validateWatchMode()
		require.NoError(t, err)
		config.NoSave = true
		_, err = validateWatchMode()
		require.Error(t, err)
		config.NoSave = false
		config.Watch = "file:"
		_, err = validateWatchMode()
		require.Error(t, err)
		config.Watch = "process"
		config.WatchMaxRequests = 100
		config.WatchTimeout = "0s"
		_, err = validateWatchMode()
		require.Error(t, err)
	})
}

func TestWatchPausedPendingEventStillHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m, err := watch.New(ctx, "", time.Second)
	require.NoError(t, err)
	defer m.Close()
	model := &watchWaitModel{ctx: ctx, manager: m}
	m.Pause(true)
	_, cmd := model.Update(watchEventMsg{event: watch.Event{Output: "pending"}})
	require.Nil(t, cmd)
	require.NotNil(t, model.pending)
	require.False(t, model.done)
	cancel()
	_, cmd = model.Update(watchTickMsg{})
	require.NotNil(t, cmd)
	require.True(t, model.done)
	require.ErrorIs(t, model.err, context.Canceled)
}

package cli

import (
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestFormatStatsDuration(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0ms"},
		{-5 * time.Millisecond, "0ms"},
		{200 * time.Microsecond, "<1ms"},
		{42 * time.Millisecond, "42ms"},
		{999*time.Millisecond + 600*time.Microsecond, "1.00s"},
		{1234 * time.Millisecond, "1.23s"},
		{9990 * time.Millisecond, "9.99s"},
		{12340 * time.Millisecond, "12.3s"},
		{90 * time.Second, "1m30s"},
		{65*time.Second + 400*time.Millisecond, "1m5s"},
	} {
		require.Equal(t, tc.want, formatStatsDuration(tc.d), tc.d.String())
	}
}

func TestOutputTokensPerSecond(t *testing.T) {
	require.Empty(t, outputTokensPerSecond(proto.TokenUsage{}, time.Second))
	require.Empty(t, outputTokensPerSecond(proto.TokenUsage{InputTokens: 10, OutputTokens: 0, TotalTokens: 10}, time.Second))
	require.Empty(t, outputTokensPerSecond(proto.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, 50*time.Millisecond))
	require.Equal(t, "42.5", outputTokensPerSecond(proto.TokenUsage{InputTokens: 10, OutputTokens: 85, TotalTokens: 95}, 2*time.Second))
	require.Equal(t, "150", outputTokensPerSecond(proto.TokenUsage{InputTokens: 10, OutputTokens: 300, TotalTokens: 310}, 2*time.Second))
}

func TestFormatInteractionStats(t *testing.T) {
	t.Run("zero stats show only unavailable tokens", func(t *testing.T) {
		require.Equal(t, "Token usage: unavailable", formatInteractionStats(InteractionStats{}))
	})

	t.Run("single-round simple interaction", func(t *testing.T) {
		stats := InteractionStats{
			Provider:      "openai",
			Model:         "gpt-5",
			Usage:         proto.TokenUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
			Duration:      1250 * time.Millisecond,
			FirstResponse: 180 * time.Millisecond,
			ModelDuration: 1200 * time.Millisecond,
			ModelRounds:   1,
		}
		want := "Token usage: input 100, output 50, total 150, 41.7 tok/s\n" +
			"Time: 1.25s, first response 180ms\n" +
			"Run: openai/gpt-5, 1 round, 0 tools"
		require.Equal(t, want, formatInteractionStats(stats))
	})

	t.Run("multi-round with tools outcomes review retry and compaction", func(t *testing.T) {
		stats := InteractionStats{
			Provider:       "anthropic",
			Model:          "claude-sonnet",
			Usage:          proto.TokenUsage{InputTokens: 1200, CachedInputTokens: 400, OutputTokens: 300, ReasoningOutputTokens: 50, TotalTokens: 1500},
			Duration:       5 * time.Second,
			FirstResponse:  250 * time.Millisecond,
			ModelDuration:  2 * time.Second,
			ReviewDuration: 1500 * time.Millisecond,
			ModelRounds:    3,
			Retries:        2,
			Compactions:    1,
			Tools: ToolStats{
				Total:       4,
				Rounds:      2,
				Succeeded:   2,
				NonZeroExit: 1,
				Denied:      1,
				Duration:    1200 * time.Millisecond,
			},
		}
		want := "Token usage: input 1,200, output 300, total 1,500, cached input 400, reasoning output 50, 150 tok/s\n" +
			"Time: 5.00s, model 2.00s, tools 1.20s, review 1.50s, first response 250ms\n" +
			"Run: anthropic/claude-sonnet, 3 rounds, 4 tools (2 ok, 1 non-zero, 1 denied), 2 retries, 1 compaction"
		require.Equal(t, want, formatInteractionStats(stats))
	})

	t.Run("unavailable tokens with timing and run stats", func(t *testing.T) {
		stats := InteractionStats{
			Provider:      "ollama",
			Model:         "llama3.1",
			Duration:      800 * time.Millisecond,
			ModelDuration: 750 * time.Millisecond,
			ModelRounds:   1,
			Tools: ToolStats{
				Total:     1,
				Succeeded: 1,
				Duration:  25 * time.Millisecond,
			},
		}
		want := "Token usage: unavailable\n" +
			"Time: 800ms, model 750ms, tools 25ms\n" +
			"Run: ollama/llama3.1, 1 round, 1 tool"
		require.Equal(t, want, formatInteractionStats(stats))
	})
}

func TestStyledInteractionStats(t *testing.T) {
	styles := Styles{
		Comment: lipgloss.NewStyle().Foreground(lipgloss.Color("#757575")),
		Flag: lipgloss.NewStyle().
			Foreground(lipgloss.Color("#3EEFCF")).
			Bold(true),
	}

	require.Equal(t, "  Tokens  unavailable", ansi.Strip(styledInteractionStats(InteractionStats{}, styles)))

	stats := InteractionStats{
		Provider:       "openai",
		Model:          "gpt-5",
		Usage:          proto.TokenUsage{InputTokens: 1000, OutputTokens: 200, TotalTokens: 1200},
		Duration:       3500 * time.Millisecond,
		FirstResponse:  220 * time.Millisecond,
		ModelDuration:  2 * time.Second,
		ReviewDuration: 500 * time.Millisecond,
		ModelRounds:    2,
		Retries:        1,
		Compactions:    2,
		Tools: ToolStats{
			Total:     2,
			Succeeded: 1,
			Failed:    1,
			Duration:  600 * time.Millisecond,
		},
	}
	got := styledInteractionStats(stats, styles)
	wantStripped := "  Tokens  1,000 input  ·  200 output  ·  1,200 total  ·  100 tok/s\n" +
		"  Time    3.50s total  ·  2.00s model  ·  600ms tools  ·  500ms review  ·  220ms first response\n" +
		"  Run     openai/gpt-5  ·  2 rounds  ·  2 tools (1 ok, 1 failed)  ·  1 retry  ·  2 compactions"
	require.Equal(t, wantStripped, ansi.Strip(got))
	require.NotEqual(t, ansi.Strip(got), got)
	require.Contains(t, got, styles.Flag.Render("openai/gpt-5"))
	require.Contains(t, got, styles.Flag.Render("3.50s"))
}

func TestPrintInteractionStatsDisabledPrintsNothing(t *testing.T) {
	savedConfig := config
	defer func() { config = savedConfig }()
	config.ShowTokenUsage = false

	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			printInteractionStats(&Mods{})
		})
	})
	require.Empty(t, stdout)
	require.Empty(t, stderr)
}

package self

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShouldTriggerAutoImprove(t *testing.T) {
	cases := []struct {
		name      string
		rating    int
		threshold int
		want      bool
	}{
		{"unset threshold defaults to 3, low rating triggers", 2, 0, true},
		{"unset threshold, rating at default triggers", 3, 0, true},
		{"unset threshold, high rating does not trigger", 4, 0, false},
		{"explicit threshold 2, rating below triggers", 1, 2, true},
		{"explicit threshold 2, rating above does not", 3, 2, false},
		{"explicit threshold 5, anything triggers", 5, 5, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ShouldTriggerAutoImprove(tc.rating, tc.threshold))
		})
	}
}

func TestAutomaticEvolutionPromptBoundariesAndFields(t *testing.T) {
	got := AutomaticEvolutionPrompt(EvolutionPromptInput{
		Workspace:       "/ws/mods",
		ConversationID:  "abc123",
		Rating:          2,
		Feedback:        "the identity text is too verbose",
		OriginalRequest: "explain yourself",
		ModelOutput:     "I am a terminal AI agent ...",
	})

	require.Contains(t, got, "Work only inside the self layer (internal/self)")
	require.Contains(t, got, "Do not edit files outside internal/self")
	require.Contains(t, got, "using only files inside internal/self")
	require.NotContains(t, got, "Work only inside the current mods workspace")
	require.Contains(t, got, "Workspace: /ws/mods")
	require.Contains(t, got, "Conversation ID: abc123")
	require.Contains(t, got, "Rating: 2")
	require.Contains(t, got, "User Feedback:\nthe identity text is too verbose")
	require.Contains(t, got, "Original Request:\nexplain yourself")
	require.Contains(t, got, "Model Output:\nI am a terminal AI agent ...")
}

func TestAutomaticEvolutionPromptEmptySectionsFallback(t *testing.T) {
	got := AutomaticEvolutionPrompt(EvolutionPromptInput{
		Workspace:      "/ws",
		ConversationID: "id",
		Rating:         1,
	})
	require.Contains(t, got, "User Feedback:\n(empty)")
	require.Contains(t, got, "Original Request:\n(empty)")
	require.Contains(t, got, "Model Output:\n(empty)")
	require.True(t, strings.HasPrefix(got, "Improve mods based on the completed session evaluation."))
}

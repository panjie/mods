package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInvocationOptionsIsolateTurns(t *testing.T) {
	cfg := Config{Images: []string{"first.png"}, Compact: true, ContinueLast: true}
	cfg.Roles = map[string][]string{"role": {"original"}}
	cfg.APIs = APIs{{Models: map[string]Model{"model": {ExtraParams: map[string]any{"nested": []any{map[string]any{"value": "original"}}}}}}}
	options := NewInvocationOptions(cfg)
	first := options.ForTurn(cfg.InitialInput(), "")
	first.Roles["role"][0] = "changed"
	first.APIs[0].Models["model"].ExtraParams["nested"].([]any)[0].(map[string]any)["value"] = "changed"
	first.Images[0] = "changed.png"
	first.SessionWriteToID = "runtime"
	second := options.ForTurn(TurnInput{Prompt: "next"}, "saved")
	require.Empty(t, second.Images)
	require.False(t, second.Compact)
	require.False(t, second.ContinueLast)
	require.Equal(t, "saved", second.Continue)
	require.Empty(t, second.SessionWriteToID)
	require.Equal(t, "original", second.Roles["role"][0])
	require.Equal(t, "original", second.APIs[0].Models["model"].ExtraParams["nested"].([]any)[0].(map[string]any)["value"])
	require.Equal(t, "first.png", cfg.Images[0])
	require.Equal(t, "original", cfg.Roles["role"][0])
	// Changing parsed settings after freezing must not affect future turns.
	cfg.Roles["role"][0] = "later mutation"
	require.Equal(t, "original", options.ForTurn(TurnInput{}, "").Roles["role"][0])
}

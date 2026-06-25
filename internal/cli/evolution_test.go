package cli

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/evolution"
	"github.com/stretchr/testify/require"
)

func TestEvolutionFlagsRegistered(t *testing.T) {
	ensureTestFlags()
	require.NotNil(t, rootCmd.Flags().Lookup("evolve-auto"))
	require.NotNil(t, rootCmd.Flags().Lookup("evolve-threshold"))

	for _, name := range []string{
		"feedback",
		"feedback-kind",
		"feedback-list",
		"evolve-feedback-only",
		"feedback-propose",
		"evolution-list",
		"proposal-list",
		"proposal-show",
		"proposal-approve",
		"proposal-reject",
		"proposal-execute",
		"reason",
	} {
		require.Nil(t, rootCmd.Flags().Lookup(name), name)
	}
}

func TestEvolveThresholdFlag(t *testing.T) {
	var threshold int
	flag := newRatingThresholdFlag(3, &threshold)
	require.Equal(t, 3, threshold)
	require.NoError(t, flag.Set("1"))
	require.Equal(t, 1, threshold)
	require.NoError(t, flag.Set("5"))
	require.Equal(t, 5, threshold)
	require.Error(t, flag.Set("0"))
	require.Error(t, flag.Set("6"))
	require.Error(t, flag.Set("bad"))
}

func TestShouldTriggerAutoImprove(t *testing.T) {
	cfg := Config{EvolveAuto: true, EvolveThreshold: 3}
	require.True(t, shouldTriggerAutoImprove(&cfg, 3))
	require.True(t, shouldTriggerAutoImprove(&cfg, 2))
	require.False(t, shouldTriggerAutoImprove(&cfg, 4))

	cfg.EvolveAuto = false
	require.False(t, shouldTriggerAutoImprove(&cfg, 1))
}

func TestShouldPromptEvolutionEvaluationConditions(t *testing.T) {
	oldInputTTY := IsInputTTY
	oldOutputTTY := IsOutputTTY
	t.Cleanup(func() {
		IsInputTTY = oldInputTTY
		IsOutputTTY = oldOutputTTY
	})
	IsInputTTY = func() bool { return true }
	IsOutputTTY = func() bool { return true }

	cfg := Config{EvolveAuto: true, CacheWriteToID: "conversation"}
	require.True(t, shouldPromptEvolutionEvaluation(&cfg))

	cfg.Quiet = true
	require.False(t, shouldPromptEvolutionEvaluation(&cfg))
	cfg.Quiet = false
	cfg.Raw = true
	require.False(t, shouldPromptEvolutionEvaluation(&cfg))
	cfg.Raw = false
	cfg.NoCache = true
	require.False(t, shouldPromptEvolutionEvaluation(&cfg))
	cfg.NoCache = false
	cfg.CacheWriteToID = ""
	require.False(t, shouldPromptEvolutionEvaluation(&cfg))

	cfg.CacheWriteToID = "conversation"
	cfg.EvolveAuto = false
	require.False(t, shouldPromptEvolutionEvaluation(&cfg))
	cfg.EvolveAuto = true
	IsInputTTY = func() bool { return false }
	require.False(t, shouldPromptEvolutionEvaluation(&cfg))
}

func TestMaybeCollectEvolutionEvaluationHighScoreOnlyRecords(t *testing.T) {
	oldInputTTY := IsInputTTY
	oldOutputTTY := IsOutputTTY
	oldForm := runEvolutionEvaluationForm
	oldAutoImprove := runEvolutionAutoImprove
	t.Cleanup(func() {
		IsInputTTY = oldInputTTY
		IsOutputTTY = oldOutputTTY
		runEvolutionEvaluationForm = oldForm
		runEvolutionAutoImprove = oldAutoImprove
	})
	IsInputTTY = func() bool { return true }
	IsOutputTTY = func() bool { return true }
	runEvolutionEvaluationForm = func(*Config) (evolutionEvaluationInput, error) {
		return evolutionEvaluationInput{Feedback: "looks good", Rating: 5}, nil
	}
	autoCalled := false
	runEvolutionAutoImprove = func(context.Context, *Mods, *DB, evolution.Evaluation) error {
		autoCalled = true
		return nil
	}

	db := testDB(t)
	cfg := Config{
		EvolveAuto:      true,
		EvolveThreshold: 3,
		CacheWriteToID:  "conversation",
	}
	cfg.BuiltinTools.Workspace = t.TempDir()
	mods := &Mods{Config: &cfg}

	require.NoError(t, maybeCollectEvolutionEvaluation(context.Background(), mods, db))
	require.False(t, autoCalled)

	evaluations, err := db.ListEvolutionEvaluations(cfg.ResolveWorkspaceRoot())
	require.NoError(t, err)
	require.Len(t, evaluations, 1)
	require.Equal(t, evolution.EvaluationRecorded, evaluations[0].Status)
	require.False(t, evaluations[0].Triggered)
	require.Equal(t, "looks good", evaluations[0].Feedback)
}

func TestMaybeCollectEvolutionEvaluationAutoImprove(t *testing.T) {
	oldInputTTY := IsInputTTY
	oldOutputTTY := IsOutputTTY
	oldForm := runEvolutionEvaluationForm
	oldAutoImprove := runEvolutionAutoImprove
	t.Cleanup(func() {
		IsInputTTY = oldInputTTY
		IsOutputTTY = oldOutputTTY
		runEvolutionEvaluationForm = oldForm
		runEvolutionAutoImprove = oldAutoImprove
	})
	IsInputTTY = func() bool { return true }
	IsOutputTTY = func() bool { return true }
	runEvolutionEvaluationForm = func(*Config) (evolutionEvaluationInput, error) {
		return evolutionEvaluationInput{Feedback: "fix it", Rating: 2}, nil
	}
	autoCalled := false
	runEvolutionAutoImprove = func(_ context.Context, _ *Mods, _ *DB, got evolution.Evaluation) error {
		autoCalled = true
		require.True(t, got.Triggered)
		return nil
	}

	db := testDB(t)
	cfg := Config{
		EvolveAuto:      true,
		EvolveThreshold: 3,
		CacheWriteToID:  "conversation",
	}
	cfg.BuiltinTools.Workspace = t.TempDir()
	mods := &Mods{Config: &cfg}

	require.NoError(t, maybeCollectEvolutionEvaluation(context.Background(), mods, db))
	require.True(t, autoCalled)

	evaluations, err := db.ListEvolutionEvaluations(cfg.ResolveWorkspaceRoot())
	require.NoError(t, err)
	require.Len(t, evaluations, 1)
	require.Equal(t, evolution.EvaluationVerified, evaluations[0].Status)
	require.True(t, evaluations[0].Triggered)
}

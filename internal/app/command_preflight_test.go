package app

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

// The command preflight is advisory: it nudges the model toward command shapes
// that are easier to review, but it never stops a call. Any test here that
// expects a nudge must also be clear that the call still reaches approval.

func compoundAssessment() approval.CommandAssessment {
	return approval.CommandAssessment{
		Effect: approval.EffectWrite,
		Shape:  approval.CommandShape{TopLevelActions: 4},
		Reviewability: approval.CommandReviewability{
			Level:   approval.ReviewabilityCompound,
			Reasons: []approval.ReviewabilityReason{approval.ReviewabilityMultipleIndependent},
		},
	}
}

func TestCommandPreflightNudgesAreBoundedAndNeverBlock(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	gate := newCommandPreflightGate(&cfg)

	for i := 0; i < commandCorrectionBudget; i++ {
		err := gate.check("powershell_run", compoundAssessment())
		require.Error(t, err, "nudge %d", i)
		var correction correctionSuggester
		require.ErrorAs(t, err, &correction)
		require.True(t, correction.CorrectionSuggested())
		require.Contains(t, err.Error(), "4 top-level actions")
	}
	require.NoError(t, gate.check("powershell_run", compoundAssessment()),
		"an exhausted budget defers to approval instead of stopping the call")
	require.NoError(t, gate.check("shell_run", compoundAssessment()))
}

func TestCommandPreflightSkipsReadsAndPreWrittenScripts(t *testing.T) {
	cfg := defaultConfig()
	read := approval.CommandAssessment{
		Effect:     approval.EffectRead,
		StaticRead: true,
		Shape:      approval.CommandShape{TopLevelActions: 5, Opaque: true},
	}
	require.NoError(t, newCommandPreflightGate(&cfg).check("shell_run", read))

	script := compoundAssessment()
	script.Reviewability.ScriptFilePayload = true
	require.NoError(t, newCommandPreflightGate(&cfg).check("shell_run", script),
		"a skill's own script must run as written")
}

func TestCommandPreflightModes(t *testing.T) {
	minimal := defaultConfig()
	minimal.Minimal = true
	never := defaultConfig()
	never.ReviewMode = ReviewNever
	require.Error(t, newCommandPreflightGate(&minimal).check("shell_run", compoundAssessment()))
	require.NoError(t, newCommandPreflightGate(&never).check("shell_run", compoundAssessment()))
}

func TestCommandPreflightBudgetIsConcurrencySafe(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	gate := newCommandPreflightGate(&cfg)
	var nudges atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if gate.check("shell_run", compoundAssessment()) != nil {
				nudges.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(commandCorrectionBudget), nudges.Load())
}

func TestCommandSimplificationMessageGuidesWithoutEchoingTargets(t *testing.T) {
	assessment := approval.CommandAssessment{
		DynamicTargets: []string{"$SECRET_PROFILE"},
		Reviewability: approval.CommandReviewability{
			Level:   approval.ReviewabilityCompound,
			Reasons: []approval.ReviewabilityReason{approval.ReviewabilityDynamicWriteTarget},
		},
	}
	message := commandSimplificationMessage(assessment)
	require.Contains(t, message, "runtime-resolved path")
	require.NotContains(t, message, "$SECRET_PROFILE")

	opaque := approval.CommandAssessment{
		Shape:         approval.CommandShape{Opaque: true},
		Reviewability: approval.CommandReviewability{Level: approval.ReviewabilityOpaque},
	}
	require.Contains(t, commandSimplificationMessage(opaque), "cannot be analyzed")
}

// The nudge must not invite a rewrite of the operation itself: model rewrites of
// pre-written scripts are exactly the failure this advice has to avoid.
func TestCommandSimplificationMessageKeepsBehaviorStable(t *testing.T) {
	message := commandSimplificationMessage(compoundAssessment())
	require.Contains(t, message, "keep the operation's behavior identical")
	require.Contains(t, message, "never rewrite an existing script file")
}

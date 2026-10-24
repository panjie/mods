package commandanalysis

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/panjie/mods/internal/pathutil"
)

// probeEligibleRef matches a PowerShell engine-automatic variable reference
// that the host can report deterministically without executing the user's
// command: $PROFILE / $HOME (optionally braced), or one of the four
// $PROFILE.* path properties. A literal path tail never follows the reference
// here — engine automatics used as path roots are either bare (a file for
// $PROFILE) or handled by the external-path extractor. Only these exact shapes
// are ever sent to the probe, so the expression is rebuilt from the validated
// name/property and never from user text.
var probeEligibleRef = regexp.MustCompile(`(?i)^\$\{?(profile|home)\}?(?:\.(CurrentUserCurrentHost|CurrentUserAllHosts|AllUsersCurrentHost|AllUsersAllHosts))?$`)

// probeTimeout bounds a single one-shot probe so a hung PowerShell host cannot
// stall command assessment.
const probeTimeout = 2 * time.Second

// validProbeValue enforces the value-acceptance policy for a probe result: a
// single non-empty absolute path. Anything else means the reference cannot be
// resolved deterministically and must stay dynamic.
func validProbeValue(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	return pathutil.IsAbs(value)
}

// ResolveEngineAutomaticTargets is the legacy standalone entry point. Like
// Analyze, it resolves fresh host state on every call.
func ResolveEngineAutomaticTargets(targets []string, assigned map[string]bool) map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), analysisTimeout)
	defer cancel()
	return NewEngine(Settings{}, Dependencies{}).run(ctx).resolveAutomatics(targets, assigned)
}

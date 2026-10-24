package commandanalysis

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type automaticFunc func(context.Context, string, string, []string) (string, error)

func (f automaticFunc) Resolve(ctx context.Context, name, property string, env []string) (string, error) {
	return f(ctx, name, property, env)
}

func TestProbeEligibleRef(t *testing.T) {
	eligible := []string{
		`$PROFILE`,
		`${PROFILE}`,
		`$profile`,
		`$HOME`,
		`${HOME}`,
		`$home`,
		`$PROFILE.CurrentUserCurrentHost`,
		`$PROFILE.CurrentUserAllHosts`,
		`$PROFILE.AllUsersCurrentHost`,
		`$PROFILE.AllUsersAllHosts`,
	}
	for _, expr := range eligible {
		require.True(t, probeEligibleRef.MatchString(expr), "expected eligible: %s", expr)
	}

	ineligible := []string{
		``,
		`$target`,
		`$env:TEMP`,
		`$env:USERPROFILE`,
		`$PROFILE\foo.ps1`,
		`$HOME\.ssh`,
		`$PROFILE.ToString()`,
		`$PROFILE.PSObject`,
		`$(Get-Location)`,
		`@($PROFILE)`,
		`$PROFILES`,
		`$HOMEDIR`,
	}
	for _, expr := range ineligible {
		require.False(t, probeEligibleRef.MatchString(expr), "expected ineligible: %s", expr)
	}
}

func TestResolveEngineAutomaticTargets(t *testing.T) {
	resolve := func(_ context.Context, name, property string, _ []string) (string, error) {
		switch {
		case name == "profile" && property == "":
			return `C:\Users\Test\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`, nil
		case name == "profile" && property == "CurrentUserCurrentHost":
			return `C:\Users\Test\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`, nil
		case name == "home":
			return `C:\Users\Test`, nil
		default:
			return "", errors.New("unexpected reference")
		}
	}
	run := func() *analysisRun {
		return NewEngine(Settings{}, Dependencies{Automatics: automaticFunc(resolve)}).run(context.Background())
	}

	assigned := map[string]bool{"profile": true}
	resolved := run().resolveAutomatics(
		[]string{`$PROFILE`, `$HOME`, `$target`, `$env:TEMP`},
		assigned,
	)
	// $PROFILE is assigned in-command, so it stays unresolved; $HOME resolves;
	// $target and $env:TEMP are not engine automatics.
	require.Len(t, resolved, 1)
	require.Equal(t, `C:\Users\Test`, resolved[`$HOME`])
}

func TestResolveEngineAutomaticTargetsFailsClosed(t *testing.T) {
	resolve := func(_ context.Context, name, property string, _ []string) (string, error) {
		return `relative\path`, nil
	}
	run := func() *analysisRun {
		return NewEngine(Settings{}, Dependencies{Automatics: automaticFunc(resolve)}).run(context.Background())
	}

	require.Empty(t, run().resolveAutomatics([]string{`$PROFILE`}, nil),
		"a non-absolute probe value must leave the target unresolved")

	resolve = func(_ context.Context, name, property string, _ []string) (string, error) {
		return "", errors.New("host unavailable")
	}
	require.Empty(t, run().resolveAutomatics([]string{`$HOME`}, nil),
		"a probe failure must leave the target unresolved")

	resolve = func(_ context.Context, name, property string, _ []string) (string, error) {
		return "C:\\one\nC:\\two", nil
	}
	require.Empty(t, run().resolveAutomatics([]string{`$PROFILE`}, nil),
		"a multi-line probe value must leave the target unresolved")
}

func TestAssignedPowerShellVariables(t *testing.T) {
	ir := &psBridgeIR{
		AssignmentTargets:            []string{`$target`, `$PROFILE`},
		ScriptBlockAssignmentTargets: []string{`$target`, `$inner`},
	}
	require.Equal(t, []string{"inner", "profile", "target"}, assignedPowerShellVariables(ir))
	require.Nil(t, assignedPowerShellVariables(nil))
}

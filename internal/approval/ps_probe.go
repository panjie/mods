package approval

import (
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"sync"
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

// probeHostCommand resolves one whitelisted reference by asking a one-shot
// PowerShell child — the same host and flags the executor uses — to print its
// value. It never passes user command text to the child; the expression is
// rebuilt from the validated name/property. The returned value is the child's
// raw, trimmed stdout. Overridable in tests.
var probeHostCommand = func(name, property string) (string, error) {
	shell := getWindowsShellPath()
	if shell == "" {
		return "", errors.New("PowerShell host not available")
	}
	expression := "$" + strings.ToUpper(name)
	if property != "" {
		expression += "." + property
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "Write-Output "+expression)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// validProbeValue enforces the value-acceptance policy for a probe result: a
// single non-empty absolute path. Anything else means the reference cannot be
// resolved deterministically and must stay dynamic.
func validProbeValue(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return false
	}
	return pathutil.IsAbs(value)
}

// probeCache holds process-lifetime resolutions keyed by the canonical
// reference (variable name plus optional property). There are at most a
// handful of distinct references ($PROFILE, $HOME, and the four profile
// properties), so a simple map suffices. Only successful resolutions are
// cached; failures fall through to a retry on the next command.
var probeCache = struct {
	sync.Mutex
	values map[string]string
}{values: map[string]string{}}

func probeVariableValue(name, property string) (string, bool) {
	key := strings.ToUpper(name)
	if property != "" {
		key += "." + property
	}
	probeCache.Lock()
	if v, ok := probeCache.values[key]; ok {
		probeCache.Unlock()
		return v, true
	}
	probeCache.Unlock()

	value, err := probeHostCommand(name, property)
	if err != nil || !validProbeValue(value) {
		return "", false
	}
	probeCache.Lock()
	probeCache.values[key] = value
	probeCache.Unlock()
	return value, true
}

// ResolveEngineAutomaticTargets resolves engine-automatic dynamic targets
// ($PROFILE / $HOME and the four $PROFILE.* path properties) to their concrete
// absolute paths. It returns a map from the original target expression to the
// resolved path. Targets whose shape is not a bare reference, whose variable is
// assigned within the command (assigned), or whose value the host cannot
// report are omitted — resolution is fail-closed.
func ResolveEngineAutomaticTargets(targets []string, assigned map[string]bool) map[string]string {
	if len(targets) == 0 {
		return nil
	}
	resolved := map[string]string{}
	for _, target := range targets {
		trimmed := strings.TrimSpace(target)
		m := probeEligibleRef.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		name := strings.ToLower(m[1])
		if assigned[name] {
			continue
		}
		value, ok := probeVariableValue(name, m[2])
		if !ok {
			continue
		}
		resolved[trimmed] = value
	}
	return resolved
}

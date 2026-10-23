package commandanalysis

import (
	"errors"
	"slices"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// Input contains exactly one shell source or literal process invocation. Cwd
// describes execution context, never authority to write there.
type Input struct {
	Cwd     string
	Dialect pathutil.Flavor
	Shell   *ShellInput
	Process *ProcessInput
}

type ShellInput struct {
	Source              string
	ShadowedEnvironment map[string]bool
	// InvocationDialect preserves the legacy Git enumeration dialect during
	// phase 1. Phase 2 will replace that second parse with shared syntax facts.
	InvocationDialect pathutil.Flavor
}

type ProcessInput struct {
	Program         string
	Args            []string
	EnvironmentKeys []string
}

func (in Input) snapshot() (Input, error) {
	if (in.Shell == nil) == (in.Process == nil) {
		return Input{}, errors.New("command input requires exactly one shell or process")
	}
	if in.Dialect != pathutil.FlavorPOSIX && in.Dialect != pathutil.FlavorPowerShell {
		return Input{}, errors.New("unsupported command dialect")
	}
	if in.Shell != nil {
		v := *in.Shell
		if v.InvocationDialect != pathutil.FlavorPOSIX && v.InvocationDialect != pathutil.FlavorPowerShell {
			return Input{}, errors.New("unsupported invocation dialect")
		}
		v.ShadowedEnvironment = make(map[string]bool, len(in.Shell.ShadowedEnvironment))
		for k, value := range in.Shell.ShadowedEnvironment {
			v.ShadowedEnvironment[k] = value
		}
		in.Shell = &v
	} else {
		v := *in.Process
		if strings.TrimSpace(v.Program) == "" {
			return Input{}, errors.New("empty process program")
		}
		v.Args = slices.Clone(v.Args)
		v.EnvironmentKeys = slices.Clone(v.EnvironmentKeys)
		in.Process = &v
	}
	return in, nil
}

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

// Bound untrusted input before copying its mutable containers.
func (in Input) withinBudget() bool {
	size := len(in.Cwd)
	if in.Shell != nil {
		size += len(in.Shell.Source)
		if len(in.Shell.ShadowedEnvironment) > maxAnalysisNodes {
			return false
		}
		for name := range in.Shell.ShadowedEnvironment {
			size += len(name)
			if size > maxAnalysisInputBytes {
				return false
			}
		}
	}
	if in.Process != nil {
		if len(in.Process.Args) > maxAnalysisNodes || len(in.Process.EnvironmentKeys) > maxAnalysisNodes {
			return false
		}
		size += len(in.Process.Program)
		for _, arg := range in.Process.Args {
			size += len(arg)
			if size > maxAnalysisInputBytes {
				return false
			}
		}
		for _, key := range in.Process.EnvironmentKeys {
			size += len(key)
			if size > maxAnalysisInputBytes {
				return false
			}
		}
	}
	return size <= maxAnalysisInputBytes
}

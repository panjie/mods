package app

import (
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/providerinfo"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/tooling"
)

// Option customizes app construction without expanding the stable New
// signature for callers that do not own CLI metadata.
type Option func(*newOptions)

type newOptions struct {
	selfHelpFlags []selfhelp.FlagGroup
}

// WithSelfHelpCLI supplies a snapshot of the live visible CLI registry.
func WithSelfHelpCLI(groups []selfhelp.FlagGroup) Option {
	cloned := make([]selfhelp.FlagGroup, len(groups))
	for i, group := range groups {
		cloned[i] = selfhelp.FlagGroup{
			Name:  group.Name,
			Flags: append([]selfhelp.Flag(nil), group.Flags...),
		}
	}
	return func(options *newOptions) {
		options.selfHelpFlags = cloned
	}
}

func buildSelfHelpReference(flags []selfhelp.FlagGroup) (selfhelp.Reference, error) {
	settingInfos := cfgpkg.SelfHelpSettings()
	settings := make([]selfhelp.Setting, 0, len(settingInfos))
	for _, info := range settingInfos {
		settings = append(settings, selfhelp.Setting{
			Path:        info.Path,
			ValueType:   info.ValueType,
			Description: info.Description,
			Default:     info.Default,
		})
	}

	providerInfos := providerinfo.Descriptors()
	providers := make([]selfhelp.Provider, 0, len(providerInfos))
	for _, info := range providerInfos {
		providers = append(providers, selfhelp.Provider{
			Name:           info.Name,
			Protocol:       info.Protocol,
			Description:    info.Description,
			DefaultBaseURL: info.DefaultBaseURL,
			APIKeyEnv:      info.APIKeyEnv,
		})
	}

	toolInfos, err := tooling.BuiltinSpecs()
	if err != nil {
		return selfhelp.Reference{}, err
	}
	tools := make([]selfhelp.Tool, 0, len(toolInfos))
	for _, info := range toolInfos {
		tools = append(tools, selfhelp.Tool{
			Name:        info.Name,
			Description: info.Description,
			Kind:        string(info.Kind),
			ReadOnly:    info.ReadOnly,
			Mutable:     info.Mutable,
			Shell:       info.Shell,
			Interactive: info.Interactive,
		})
	}

	return selfhelp.NewReference(selfhelp.Catalog{
		Flags:     flags,
		Settings:  settings,
		Providers: providers,
		Protocols: providerinfo.Protocols(),
		Tools:     tools,
	}), nil
}

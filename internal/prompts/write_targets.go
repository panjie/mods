package prompts

import _ "embed"

// WriteTargets is internal policy, not a configurable prompt or feature flag.
//
//go:embed write_targets.md
var WriteTargets string

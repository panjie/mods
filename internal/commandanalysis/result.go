package commandanalysis

import "slices"

// Result exposes role-aware Targets. KnownDirs remains a display-only legacy
// projection; authorization belongs to the consuming adapter.
type Result = CommandAssessment

// Clone isolates both analyzer results and approved snapshots from later edits.
func (a CommandAssessment) Clone() CommandAssessment {
	a.Targets = slices.Clone(a.Targets)
	a.Gaps = slices.Clone(a.Gaps)
	a.KnownDirs = slices.Clone(a.KnownDirs)
	a.WritePaths = slices.Clone(a.WritePaths)
	a.ProviderWriteTargets = slices.Clone(a.ProviderWriteTargets)
	a.RemoteOrigins = slices.Clone(a.RemoteOrigins)
	a.UnresolvedRemoteTargets = slices.Clone(a.UnresolvedRemoteTargets)
	a.DynamicTargets = slices.Clone(a.DynamicTargets)
	a.AssignedVariables = slices.Clone(a.AssignedVariables)
	a.Reviewability.Reasons = slices.Clone(a.Reviewability.Reasons)
	if a.LiteralAssignments != nil {
		values := make(map[string]string, len(a.LiteralAssignments))
		for k, v := range a.LiteralAssignments {
			values[k] = v
		}
		a.LiteralAssignments = values
	}
	return a
}

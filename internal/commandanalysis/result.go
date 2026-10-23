package commandanalysis

import "slices"

// Result retains the existing facts during the behavior-preserving migration.
// KnownDirs is not a role-aware target model yet. Authorization belongs to the
// consuming adapter, not this package.
type Result = CommandAssessment

// Clone isolates both analyzer results and approved snapshots from later edits.
func (a CommandAssessment) Clone() CommandAssessment {
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

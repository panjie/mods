package approval

import "github.com/panjie/mods/internal/commandanalysis"

// Shared evidence vocabulary for tool-neutral policy. No command analysis or
// conversion to AccessIntent occurs in this package.
type TargetCoverage = commandanalysis.TargetCoverage
type AssessmentGap = commandanalysis.AssessmentGap

const (
	CoverageIncomplete   = commandanalysis.CoverageIncomplete
	CoverageComplete     = commandanalysis.CoverageComplete
	GapUnknownInvocation = commandanalysis.GapUnknownInvocation
	GapControlFlow       = commandanalysis.GapControlFlow
	GapPathContext       = commandanalysis.GapPathContext
	GapExecutionContext  = commandanalysis.GapExecutionContext
	GapWriteTargets      = commandanalysis.GapWriteTargets
)

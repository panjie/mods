package approval

// TargetCoverage describes structured native-tool targets. Commands do not use it.
type TargetCoverage uint8

const (
	CoverageIncomplete TargetCoverage = iota
	CoverageComplete
)

type AssessmentGap string

const GapWriteTargets AssessmentGap = "unbounded write targets"

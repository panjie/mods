package approval

// Evidence describes the authority behind a command's effect, not confidence
// reported by a language model. The zero value carries no authority.
type Evidence string

const (
	EvidenceUnknown Evidence = ""
	EvidenceStatic  Evidence = "static"
	EvidenceTrust   Evidence = "explicit trust"
)

// TargetCoverage is independent of effect: a command may certainly write while
// only some of its destinations are known. Missing coverage never grants access.
type TargetCoverage uint8

const (
	CoverageIncomplete TargetCoverage = iota
	CoverageComplete
)

type AssessmentGap string

const (
	GapUnknownInvocation AssessmentGap = "unknown invocation"
	GapControlFlow       AssessmentGap = "unmodeled control flow"
	GapPathContext       AssessmentGap = "mutable path context"
	GapExecutionContext  AssessmentGap = "unverified execution context"
	GapWriteTargets      AssessmentGap = "unbounded write targets"
)

func (a *CommandAssessment) AddGap(gap AssessmentGap) {
	a.WriteCoverage = CoverageIncomplete
	for _, old := range a.Gaps {
		if old == gap {
			return
		}
	}
	a.Gaps = append(a.Gaps, gap)
}

func (intent AccessIntent) HasCompleteWriteTargets() bool {
	return intent.WriteCoverage == CoverageComplete && len(intent.Gaps) == 0 &&
		!intent.HasUnresolvedPaths() && !intent.HasUnresolvedRemoteTargets() && !intent.HasProviderWriteTargets()
}

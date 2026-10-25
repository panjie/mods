package approval

import (
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/pathutil"
)

// Transitional facade; no analysis implementation lives here.
type CommandEffect = commandanalysis.CommandEffect
type CommandShape = commandanalysis.CommandShape
type CommandAssessment commandanalysis.CommandAssessment

const EffectRead = commandanalysis.EffectRead
const EffectWrite = commandanalysis.EffectWrite
const EffectUnknown = commandanalysis.EffectUnknown

func UnknownCommandAssessment() CommandAssessment {
	return CommandAssessment(commandanalysis.UnknownCommandAssessment())
}
func AssessShellStaticWithPolicy(command string, posix bool, policy ReadOnlyCommandPolicy) CommandAssessment {
	return CommandAssessment(commandanalysis.AssessShellStaticWithPolicy(command, posix, policy))
}
func AssessShellStaticWithContext(command string, posix bool, policy ReadOnlyCommandPolicy, cwd string) CommandAssessment {
	return CommandAssessment(commandanalysis.AssessShellStaticWithContext(command, posix, policy, cwd))
}
func ExternalShellPathFacts(command, cwdDir string, flavor pathutil.Flavor, policy ReadOnlyCommandPolicy) []string {
	return commandanalysis.ExternalShellPathFacts(command, cwdDir, flavor, policy)
}
func IsExplicitShellPathArg(arg string) bool { return commandanalysis.IsExplicitShellPathArg(arg) }
func IsExplicitPowerShellPathArg(arg string) bool {
	return commandanalysis.IsExplicitPowerShellPathArg(arg)
}
func IsShellPathSeparator(ch byte) bool { return commandanalysis.IsShellPathSeparator(ch) }

type Evidence = commandanalysis.Evidence
type TargetCoverage = commandanalysis.TargetCoverage
type AssessmentGap = commandanalysis.AssessmentGap

const EvidenceUnknown = commandanalysis.EvidenceUnknown
const EvidenceStatic = commandanalysis.EvidenceStatic
const EvidenceTrust = commandanalysis.EvidenceTrust
const CoverageIncomplete = commandanalysis.CoverageIncomplete
const CoverageComplete = commandanalysis.CoverageComplete
const GapUnknownInvocation = commandanalysis.GapUnknownInvocation
const GapControlFlow = commandanalysis.GapControlFlow
const GapPathContext = commandanalysis.GapPathContext
const GapExecutionContext = commandanalysis.GapExecutionContext
const GapWriteTargets = commandanalysis.GapWriteTargets

type ArgvStaticContext = commandanalysis.ArgvStaticContext
type Invocation = commandanalysis.Invocation

func ShellInvocations(command string, posix bool) ([]Invocation, bool) {
	return commandanalysis.ShellInvocations(command, posix)
}
func CloseBridge() { commandanalysis.CloseBridge() }
func ResolveEngineAutomaticTargets(targets []string, assigned map[string]bool) map[string]string {
	return commandanalysis.ResolveEngineAutomaticTargets(targets, assigned)
}
func IsReadOnlyPOSIX(command string) (bool, string) { return commandanalysis.IsReadOnlyPOSIX(command) }
func IsReadOnlyPOSIXWithPolicy(command string, policy ReadOnlyCommandPolicy) (bool, string) {
	return commandanalysis.IsReadOnlyPOSIXWithPolicy(command, policy)
}

type ReadOnlyCommandPolicy = commandanalysis.ReadOnlyCommandPolicy

func IsReadOnlyPowerShell(command string) (bool, string, []string) {
	return commandanalysis.IsReadOnlyPowerShell(command)
}
func IsReadOnlyPowerShellWithPolicy(command string, policy ReadOnlyCommandPolicy) (bool, string, []string) {
	return commandanalysis.IsReadOnlyPowerShellWithPolicy(command, policy)
}
func NormalizeRemoteOrigin(raw string) (string, bool) {
	return commandanalysis.NormalizeRemoteOrigin(raw)
}
func NormalizeRemoteOrigins(values []string) []string {
	return commandanalysis.NormalizeRemoteOrigins(values)
}

type ReviewabilityLevel = commandanalysis.ReviewabilityLevel
type ReviewabilityReason = commandanalysis.ReviewabilityReason
type CommandReviewability = commandanalysis.CommandReviewability

const ReviewabilitySimple = commandanalysis.ReviewabilitySimple
const ReviewabilityCompound = commandanalysis.ReviewabilityCompound
const ReviewabilityOpaque = commandanalysis.ReviewabilityOpaque
const ReviewabilitySingleProgramInShell = commandanalysis.ReviewabilitySingleProgramInShell
const ReviewabilityMultipleIndependent = commandanalysis.ReviewabilityMultipleIndependent
const ReviewabilityMixedReadWrite = commandanalysis.ReviewabilityMixedReadWrite
const ReviewabilityDynamicWriteTarget = commandanalysis.ReviewabilityDynamicWriteTarget
const ReviewabilityMultipleDynamicTargets = commandanalysis.ReviewabilityMultipleDynamicTargets
const ReviewabilityDecorativeOutput = commandanalysis.ReviewabilityDecorativeOutput
const ReviewabilityOpaqueExecution = commandanalysis.ReviewabilityOpaqueExecution
const ReviewabilityNestedShellHost = commandanalysis.ReviewabilityNestedShellHost
const ReviewabilityCommandPassedAsScript = commandanalysis.ReviewabilityCommandPassedAsScript

func AnalyzeCommandReviewability(command string, posix bool, policy ReadOnlyCommandPolicy) CommandReviewability {
	return commandanalysis.AnalyzeCommandReviewability(command, posix, policy)
}
func AnalyzeProcessReviewability(program string, args []string, posix bool) CommandReviewability {
	return commandanalysis.AnalyzeProcessReviewability(program, args, posix)
}
func AnalyzeProcessReviewabilityWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) CommandReviewability {
	return commandanalysis.AnalyzeProcessReviewabilityWithPolicy(program, args, posix, policy)
}
func StaticPOSIXLiteralArgs(command string) []string {
	return commandanalysis.StaticPOSIXLiteralArgs(command)
}
func POSIXHasUnquotedBareHomeArg(command string) bool {
	return commandanalysis.POSIXHasUnquotedBareHomeArg(command)
}
func AssessArgvStaticWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) CommandAssessment {
	return CommandAssessment(commandanalysis.AssessArgvStaticWithPolicy(program, args, posix, policy))
}
func AssessArgvStaticWithContext(program string, args []string, posix bool, policy ReadOnlyCommandPolicy, context ArgvStaticContext) CommandAssessment {
	return CommandAssessment(commandanalysis.AssessArgvStaticWithContext(program, args, posix, policy, context))
}
func IsPowerShellProviderPath(value string) bool {
	return commandanalysis.IsPowerShellProviderPath(value)
}
func PowerShellFilesystemProviderPath(value string) (string, bool) {
	return commandanalysis.PowerShellFilesystemProviderPath(value)
}
func IsUnresolvedShellPathExpression(value string, posix bool) bool {
	return commandanalysis.IsUnresolvedShellPathExpression(value, posix)
}

// AccessIntent converts command facts into the existing tool-neutral policy
// boundary. Unknown effects intentionally map to write.
// Deprecated: legacy fixture adapter only. Production command authorization
// uses app.commandResultToIntent with role-aware targets, never KnownDirs.
func (assessment CommandAssessment) AccessIntent() AccessIntent {
	class := AccessWrite
	if assessment.Effect == EffectRead && assessment.Evidence != EvidenceUnknown {
		class = AccessRead
	}
	return AccessIntent{
		WriteCoverage:           assessment.WriteCoverage,
		Gaps:                    append([]AssessmentGap(nil), assessment.Gaps...),
		Class:                   class,
		Dirs:                    append([]string(nil), assessment.KnownDirs...),
		ProviderWriteTargets:    append([]string(nil), assessment.ProviderWriteTargets...),
		RemoteOrigins:           append([]string(nil), assessment.RemoteOrigins...),
		UnresolvedRemoteTargets: append([]string(nil), assessment.UnresolvedRemoteTargets...),
		UnresolvedPaths:         append([]string(nil), assessment.DynamicTargets...),
		UncertainEffect:         assessment.Effect == EffectUnknown,
		DynamicProbe:            assessment.DynamicProbe,
		Reason:                  assessment.Reason,
	}
}

func (a *CommandAssessment) AddGap(gap AssessmentGap) {
	(*commandanalysis.CommandAssessment)(a).AddGap(gap)
}
func (a CommandAssessment) HasReviewabilityConcerns() bool {
	return commandanalysis.CommandAssessment(a).HasReviewabilityConcerns()
}

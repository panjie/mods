package approval

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ReviewabilityLevel describes how easily a human can understand one shell
// tool call. It is presentation and model-guidance metadata, not a security
// classification.
type ReviewabilityLevel string

const (
	ReviewabilitySimple   ReviewabilityLevel = "simple"
	ReviewabilityCompound ReviewabilityLevel = "compound"
	ReviewabilityOpaque   ReviewabilityLevel = "opaque"
)

// ReviewabilityReason is a stable reason code used by correction feedback and
// the review UI.
type ReviewabilityReason string

const (
	ReviewabilitySingleProgramInShell   ReviewabilityReason = "single_program_in_shell"
	ReviewabilityMultipleIndependent    ReviewabilityReason = "multiple_independent_actions"
	ReviewabilityMixedReadWrite         ReviewabilityReason = "mixed_read_write"
	ReviewabilityDynamicWriteTarget     ReviewabilityReason = "dynamic_write_target"
	ReviewabilityMultipleDynamicTargets ReviewabilityReason = "multiple_dynamic_targets"
	ReviewabilityDecorativeOutput       ReviewabilityReason = "decorative_output"
	ReviewabilityOpaqueExecution        ReviewabilityReason = "opaque_execution"
	ReviewabilityNestedShellHost        ReviewabilityReason = "nested_shell_host"
	ReviewabilityCommandPassedAsScript  ReviewabilityReason = "command_passed_as_script"
)

// CommandReviewability contains deterministic structural facts about shell
// source. ShouldCorrect is deliberately narrow: false does not mean the
// command is safe, only that the preflight should not ask the model to retry.
type CommandReviewability struct {
	Level           ReviewabilityLevel
	Reasons         []ReviewabilityReason
	RecommendedTool string
	ShouldCorrect   bool
}

// AnalyzeCommandReviewability parses shell source without executing it.
func AnalyzeCommandReviewability(command string, posix bool, policy ReadOnlyCommandPolicy) CommandReviewability {
	return AssessShellStaticWithPolicy(command, posix, policy).Reviewability
}

// AnalyzeProcessReviewability detects direct-process calls that merely wrap
// shell source in a shell host. Ordinary literal argv invocations are simple.
func AnalyzeProcessReviewability(program string, args []string, posix bool) CommandReviewability {
	return AnalyzeProcessReviewabilityWithPolicy(program, args, posix, ReadOnlyCommandPolicy{})
}

// AnalyzeProcessReviewabilityWithPolicy detects direct-process calls that
// either wrap shell source or accidentally pass a known executable name where
// a POSIX shell expects a script path.
func AnalyzeProcessReviewabilityWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) CommandReviewability {
	name := strings.ToLower(path.Base(strings.ReplaceAll(strings.TrimSpace(program), `\`, "/")))
	if !shellHostPrograms[name] {
		return CommandReviewability{Level: ReviewabilitySimple}
	}
	if posix && posixShellHosts[name] && processArgsPassKnownCommandAsScript(args, policy) {
		return CommandReviewability{
			Level:           ReviewabilityOpaque,
			Reasons:         []ReviewabilityReason{ReviewabilityCommandPassedAsScript},
			RecommendedTool: "process_run",
			ShouldCorrect:   true,
		}
	}
	if !processArgsContainShellSourceFlag(name, args) {
		return CommandReviewability{Level: ReviewabilitySimple}
	}
	recommended := ""
	switch {
	case posix && posixShellHosts[name]:
		recommended = "shell_run"
	case !posix && (powerShellHosts[name] || name == "cmd" || name == "cmd.exe"):
		recommended = "powershell_run"
	default:
		// The matching shell tool is not available on this host. Keep the
		// literal process invocation instead of suggesting an unusable tool.
		return CommandReviewability{Level: ReviewabilitySimple}
	}
	return CommandReviewability{
		Level:           ReviewabilityOpaque,
		Reasons:         []ReviewabilityReason{ReviewabilityOpaqueExecution},
		RecommendedTool: recommended,
		ShouldCorrect:   true,
	}
}

func processArgsPassKnownCommandAsScript(args []string, policy ReadOnlyCommandPolicy) bool {
	if len(args) == 0 {
		return false
	}
	operand := strings.TrimSpace(args[0])
	if !isBarePOSIXCommand(operand) {
		return false
	}
	return policy.matchesPOSIX(operand) || readOnlyCommands[operand] ||
		subcommandReadOnly[operand] != nil || subcommandShellCommands[operand] ||
		flagPrefixShellCommands[operand] || exactShellCommands[operand]
}

func processArgsContainShellSourceFlag(program string, args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "-c", "-command", "-encodedcommand":
			return true
		case "/c":
			return program == "cmd" || program == "cmd.exe"
		}
	}
	// PowerShell hosts bind the first positional argument as -Command, so
	// `powershell.exe Get-Content file` is shell source even without a flag.
	if powerShellHosts[program] && len(args) > 0 && !strings.HasPrefix(strings.TrimSpace(args[0]), "-") {
		return true
	}
	return false
}

func analyzePOSIXReviewabilityFile(file *syntax.File, policy ReadOnlyCommandPolicy, dynamic []string) (CommandShape, CommandReviewability) {
	shape := CommandShape{}
	result := CommandReviewability{Level: ReviewabilitySimple}
	for _, stmt := range file.Stmts {
		actions, pipelines := posixStatementShape(stmt)
		shape.TopLevelActions += actions
		shape.Pipelines += pipelines
	}

	var leaves []shellLeaf
	leavesOK := true
	for _, stmt := range file.Stmts {
		if !collectShellLeaves(stmt, &leaves) {
			leavesOK = false
			break
		}
	}
	readCount, writeCount, decorative := 0, 0, 0
	if leavesOK {
		for _, leaf := range leaves {
			if leaf.call == nil || len(leaf.call.Args) == 0 {
				continue
			}
			if read, _ := callIsReadOnly(leaf.call, policy); read {
				readCount++
			} else if posixCallKnownWriter(leaf.call) {
				writeCount++
			}
			if posixDecorativeCall(leaf.call) {
				decorative++
			}
		}
	}

	if shape.TopLevelActions > 1 {
		result.Level = ReviewabilityCompound
		result.Reasons = appendReason(result.Reasons, ReviewabilityMultipleIndependent)
	}
	if readCount > 0 && writeCount > 0 {
		result.Level = ReviewabilityCompound
		result.Reasons = appendReason(result.Reasons, ReviewabilityMixedReadWrite)
		result.ShouldCorrect = true
	}
	if decorative >= 2 {
		result.Reasons = appendReason(result.Reasons, ReviewabilityDecorativeOutput)
	}
	if len(dynamic) > 1 {
		result.Reasons = appendReason(result.Reasons, ReviewabilityMultipleDynamicTargets)
	}
	if posixDirectProgram(file) {
		result.Reasons = appendReason(result.Reasons, ReviewabilitySingleProgramInShell)
		result.RecommendedTool = "process_run"
		result.ShouldCorrect = true
	}
	if shape.TopLevelActions >= 3 || shape.TopLevelActions > 1 && len(dynamic) > 0 {
		result.ShouldCorrect = true
	}
	for _, leaf := range leaves {
		if leaf.call == nil || len(leaf.call.Args) == 0 {
			continue
		}
		name, ok := staticShellWord(leaf.call.Args[0])
		if ok && (name == "eval" || name == "exec" || shellHostPrograms[name]) {
			shape.Opaque = true
			result = opaqueReviewability()
			result.Reasons = []ReviewabilityReason{ReviewabilityNestedShellHost}
			result.ShouldCorrect = true
			break
		}
	}
	return shape, result
}

func posixStatementShape(stmt *syntax.Stmt) (actions, pipelines int) {
	if stmt == nil || stmt.Cmd == nil {
		return 0, 0
	}
	binary, ok := stmt.Cmd.(*syntax.BinaryCmd)
	if !ok {
		return 1, 0
	}
	leftActions, leftPipelines := posixStatementShape(binary.X)
	rightActions, rightPipelines := posixStatementShape(binary.Y)
	if binary.Op.String() == "|" || binary.Op.String() == "|&" {
		return 1, leftPipelines + rightPipelines + 1
	}
	return leftActions + rightActions, leftPipelines + rightPipelines
}

func posixCallKnownWriter(call *syntax.CallExpr) bool {
	if call == nil || len(call.Args) == 0 {
		return false
	}
	tokens := shellWordsForAccess(call.Args)
	if len(tokens) == 0 {
		return false
	}
	return analyzeWritableTargetsFromTokens(tokens, true).Known || hasKnownRiskyInvocation(tokens, true)
}

func posixDecorativeCall(call *syntax.CallExpr) bool {
	if call == nil || len(call.Args) == 0 {
		return false
	}
	name, ok := staticShellWord(call.Args[0])
	return ok && (name == "echo" || name == "printf")
}

func posixDirectProgram(file *syntax.File) bool {
	if file == nil || len(file.Stmts) != 1 {
		return false
	}
	stmt := file.Stmts[0]
	if stmt == nil || stmt.Cmd == nil || stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
		return false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || len(call.Assigns) > 0 || shellNodeHasDynamicParts(call) {
		return false
	}
	name, ok := staticShellWord(call.Args[0])
	if !ok || !isBarePOSIXCommand(name) || posixShellBuiltins[name] || shellHostPrograms[name] {
		return false
	}
	for _, word := range call.Args[1:] {
		if _, ok := staticShellWord(word); !ok {
			return false
		}
	}
	return true
}

func analyzePowerShellReviewabilityIR(ir *psBridgeIR, policy ReadOnlyCommandPolicy, dynamic []string) (CommandShape, CommandReviewability) {
	shape := CommandShape{
		TopLevelActions: ir.TopLevelStatementCount,
		Pipelines:       ir.PipelineCount,
	}
	result := CommandReviewability{
		Level: ReviewabilitySimple,
	}
	if reason, opaque := powerShellIROpaque(ir); opaque {
		shape.Opaque = true
		result = opaqueReviewability()
		result.Reasons = []ReviewabilityReason{reason}
		result.ShouldCorrect = true
		return shape, result
	}
	if shape.TopLevelActions == 0 && len(ir.Invocations) > 0 {
		shape.TopLevelActions = 1
	}

	readCount, writeCount, decorative := 0, 0, 0
	for _, inv := range ir.Invocations {
		if readOnlyPowerShellInvocation(inv, policy) {
			readCount++
		} else {
			tokens := append([]string{inv.Name}, inv.Args...)
			if analyzeWritableTargetsFromTokens(tokens, false).Known || hasKnownRiskyInvocation(tokens, false) {
				writeCount++
			}
		}
		if powerShellDecorativeInvocation(inv) {
			decorative++
		}
	}

	if shape.TopLevelActions > 1 {
		result.Level = ReviewabilityCompound
		result.Reasons = appendReason(result.Reasons, ReviewabilityMultipleIndependent)
	}
	if readCount > 0 && writeCount > 0 {
		result.Level = ReviewabilityCompound
		result.Reasons = appendReason(result.Reasons, ReviewabilityMixedReadWrite)
		result.ShouldCorrect = true
	}
	if decorative >= 2 {
		result.Reasons = appendReason(result.Reasons, ReviewabilityDecorativeOutput)
	}
	if len(dynamic) > 1 {
		result.Reasons = appendReason(result.Reasons, ReviewabilityMultipleDynamicTargets)
	}
	if powerShellDirectProgram(ir) {
		result.Reasons = appendReason(result.Reasons, ReviewabilitySingleProgramInShell)
		result.RecommendedTool = "process_run"
		result.ShouldCorrect = true
	}
	if shape.TopLevelActions >= 3 || shape.TopLevelActions > 1 && len(dynamic) > 0 {
		result.ShouldCorrect = true
	}
	return shape, result
}

// powerShellIROpaque reports whether the PowerShell IR is opaque and, when it
// is, the reviewability reason. A nested shell host invocation gets its own
// reason so correction feedback can name the wrapping explicitly.
func powerShellIROpaque(ir *psBridgeIR) (ReviewabilityReason, bool) {
	if ir == nil || ir.HasStopParsing {
		return ReviewabilityOpaqueExecution, true
	}
	for _, flag := range ir.RiskFlags {
		if flag == "invoke_expression" || flag == "syntax_error" {
			return ReviewabilityOpaqueExecution, true
		}
	}
	for _, inv := range ir.Invocations {
		name := normalizePowerShellCommandName(inv.Name)
		if shellHostPrograms[name] && processArgsContainShellSourceFlag(name, inv.Args) {
			return ReviewabilityNestedShellHost, true
		}
	}
	return "", false
}

func powerShellDirectProgram(ir *psBridgeIR) bool {
	if ir == nil || ir.TopLevelStatementCount > 1 || ir.PipelineCount > 1 || len(ir.Invocations) != 1 ||
		ir.HasAssignment || ir.HasScriptBlock || ir.HasControlFlow || ir.HasStopParsing || len(ir.Redirects) > 0 || len(ir.Expansions) > 0 {
		return false
	}
	inv := ir.Invocations[0]
	name := normalizePowerShellCommandName(inv.Name)
	if !isBarePowerShellCommand(inv.Name) || readOnlyPowerShellCmdlets[name] || powerShellStateCommands[name] || shellHostPrograms[name] || looksLikePowerShellCmdlet(name) {
		return false
	}
	for _, arg := range inv.Args {
		if shellPathExpressionUnresolved(arg, false) || strings.ContainsAny(arg, "{}|;") {
			return false
		}
	}
	return true
}

func looksLikePowerShellCmdlet(name string) bool {
	verb, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(name)), "-")
	return ok && powerShellCmdletVerbs[verb]
}

func powerShellDecorativeInvocation(inv psCommandInvocation) bool {
	switch normalizePowerShellCommandName(inv.Name) {
	case "write-output", "write-host", "format-table", "format-list", "format-wide", "format-custom":
		return true
	default:
		return false
	}
}

func opaqueReviewability() CommandReviewability {
	return CommandReviewability{
		Level:   ReviewabilityOpaque,
		Reasons: []ReviewabilityReason{ReviewabilityOpaqueExecution},
	}
}

func appendReason(reasons []ReviewabilityReason, reason ReviewabilityReason) []ReviewabilityReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

var posixShellBuiltins = map[string]bool{
	"cd": true, "echo": true, "printf": true, "test": true, "[": true,
	"true": true, "false": true, "pwd": true, "read": true, "export": true,
	"unset": true, "set": true, "alias": true, "unalias": true, "type": true,
	"command": true, "eval": true, "exec": true, "trap": true, "umask": true,
}

var powerShellStateCommands = map[string]bool{
	"set-location": true, "cd": true, "chdir": true, "sl": true,
	"push-location": true, "pushd": true, "pop-location": true, "popd": true,
}

var shellHostPrograms = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
	"cmd": true, "cmd.exe": true, "powershell": true, "powershell.exe": true,
	"pwsh": true, "pwsh.exe": true,
}

var posixShellHosts = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true,
}

var powerShellHosts = map[string]bool{
	"powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true,
}

var powerShellCmdletVerbs = map[string]bool{
	"add": true, "clear": true, "compare": true, "convertfrom": true, "convertto": true,
	"copy": true, "disable": true, "dismount": true, "enable": true, "export": true,
	"format": true, "get": true, "grant": true, "group": true, "import": true,
	"install": true, "invoke": true, "join": true, "measure": true, "move": true,
	"mount": true, "new": true, "out": true, "pop": true, "push": true,
	"read": true, "register": true, "remove": true, "rename": true, "resolve": true,
	"restart": true, "revoke": true, "select": true, "set": true, "sort": true,
	"split": true, "start": true, "stop": true, "test": true, "uninstall": true,
	"unregister": true, "update": true, "where": true, "write": true,
}

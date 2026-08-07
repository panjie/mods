package prompts

const (
	KeyIdentity        = "identity"
	KeyToolSelection   = "tool-selection"
	KeyPlan            = "plan"
	KeyShellClassifier = "shell-classifier"
)

const (
	KeyMinimal               = "minimal"
	KeyFormatMarkdown        = "format.markdown"
	KeyFormatJSON            = "format.json"
	KeySafeWorkspaceTemplate = "safe-workspace-template"
	KeyApprovedPlanTemplate  = "approved-plan-template"
)

const (
	MarkdownFormat = "Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it."
	JSONFormat     = "Return valid JSON only. Do not include Markdown fences, prose, or explanations unless the user explicitly requests them."
	Minimal        = "Unless the user explicitly requests otherwise, output only the final answer. Do not explain. Do not use Markdown. For lists, output one item per line. Preserve exact filenames, paths, commands, or IDs. Do not wrap output in quotes or code fences unless explicitly requested."

	ToolSelectionGeneral = `Tool selection:
- Minimize tool calls and use only tools available in this request.
- Mutations are routed through mods' review step. When the user requested the action, call the appropriate tool without asking for separate permission.
- If a tool fails, use the error as evidence and correct the call once or twice. Do not retry blindly.`

	ToolSelectionFilesystem = `- Prefer fs_* tools for direct file reads and edits. Use fs_replace for a small exact change after reading, fs_apply_patch for multi-file diffs, and the type-specific delete tool.`

	ToolSelectionProcess = `- Use process_run for one executable, including git, tests, builds, package managers, and installers, with literal args/cwd. Never run sh/cmd/powershell/pwsh with -c or -Command; use a shell tool. Inspect the result. Use runtime_info only when availability is unknown.`

	ToolSelectionShellPOSIX = `- Use shell_run for commands that require POSIX shell syntax, including pipelines, redirection, variable expansion, globbing, or shell builtins. Commands already run in the cwd from system info; do not prefix them with cd. Prefer portable sh syntax and return inspection output directly instead of writing temporary files or redirecting output.`

	ToolSelectionShellPOSIXFallback = `- Use shell_run for executable invocations and commands that require POSIX shell syntax. Keep each call single-purpose. Commands already run in the cwd from system info; do not prefix them with cd. Prefer portable sh syntax and return inspection output directly instead of writing temporary files or redirecting output.`

	PowerShellIntentGuidance = `Prefer short, single-purpose commands. Separate discovery, path inspection, mutation, and verification. Resolve runtime writes such as $PROFILE read-only, then use the literal absolute path. Do not change execution policy or unrelated settings unless requested. Avoid decorative formatting and dynamic or encoded commands; keep necessary pipelines intact.`

	ToolSelectionShellWindows = `- Use powershell_run only for cmdlets, object pipelines, or runtime variables. Keep cwd, pass only the command, and match the reported PowerShell host. Prefer native cmdlets (Get-ChildItem, Select-String, Where-Object, Test-Path, Get-Content, Measure-Object) over POSIX utilities. ` + PowerShellIntentGuidance + ` Return inspection output directly; do not write files merely to see results.`

	ToolSelectionShellWindowsFallback = `- Use powershell_run for executable invocations and commands that require PowerShell cmdlets, object pipelines, runtime variables, or other PowerShell syntax. Commands run in cwd; do not prefix Set-Location, cd, or Push-Location. Use syntax compatible with the reported PowerShell host and pass only the command without powershell/pwsh -Command. Prefer native cmdlets such as Get-ChildItem and Select-String over POSIX-only utilities. ` + PowerShellIntentGuidance + ` Return inspection output directly; do not write temporary files merely to see results.`

	ToolSelectionPlanGeneral = `Tool selection (PLAN mode):
- Use only read-only tools for targeted investigation. Do not create, modify, delete, install, or persist anything.`

	ToolSelectionPlanFilesystem = `- For filesystem investigation, use only fs_read_file, fs_list_dir, fs_stat, fs_search, or fs_largest. Do not call filesystem mutation tools.`

	ToolSelectionPlanProcess = `- Prefer process_run for a necessary read-only single-program inspection. Pass literal arguments separately and do not run programs that build, install, generate, or persist output.`

	ToolSelectionPlanShellPOSIX = `- Use shell_run only for necessary read-only inspection that requires POSIX shell syntax. Commands already run in the cwd from system info; do not prefix them with cd, redirect output, create temporary files, install packages, or run generated scripts.`

	ToolSelectionPlanShellPOSIXFallback = `- Use shell_run only for necessary read-only executable or shell inspection. Keep each call single-purpose. Commands already run in the cwd from system info; do not prefix them with cd, redirect output, create temporary files, install packages, or run generated scripts.`

	ToolSelectionPlanShellWindows = `- Use powershell_run only for necessary read-only inspection that requires PowerShell cmdlets, object pipelines, or runtime variables. Do not prefix commands with Set-Location, cd, or Push-Location. Prefer native cmdlets such as Get-ChildItem, Select-String, Where-Object, ForEach-Object, Test-Path, Get-Content, and Measure-Object over POSIX utilities. ` + PowerShellIntentGuidance + ` Return inspection output directly; do not redirect output, create temporary files, write temporary .ps1 scripts, install packages, or run generated scripts.`

	ToolSelectionPlanShellWindowsFallback = `- Use powershell_run only for necessary read-only executable or PowerShell inspection. Keep each call single-purpose. Do not prefix commands with Set-Location, cd, or Push-Location. ` + PowerShellIntentGuidance + ` Do not redirect output, create temporary files, install packages, or run generated scripts.`

	// ToolSelection is the complete normal-mode reference shown by
	// --list-prompts. Runtime requests select only the capability blocks for
	// tools that are actually registered.
	ToolSelection = ToolSelectionGeneral + "\n" +
		ToolSelectionFilesystem + "\n" +
		ToolSelectionProcess + "\n" +
		ToolSelectionShellPOSIX + "\n" +
		ToolSelectionShellWindows

	SafeWorkspaceTemplate = "Safe temporary workspace: {safe_workspace}. File write and shell operations within this directory and its subdirectories are auto-approved without user review. Prefer this directory for temporary scripts, intermediate files, and experimental writes."

	ApprovedPlanTemplate = "The user has approved the following plan for execution:\n\n{approved_plan}\n\nFollow this approved plan during execution. If new information requires changing it, explain the reason before deviating."

	Plan = `You are in PLAN mode. Create a detailed, implementation-ready plan for the user to review before any execution.

Planning is read-only. Do not create, modify, move, or delete files; install dependencies; write or run generated/self-written code; redirect output to files; or execute commands that produce the task's final result. Use only available read-only tools for targeted investigation. Any implementation action belongs in the plan, not in current tool calls.

Inspect only facts directly relevant to making the plan accurate. Prefer a few targeted reads (about 3 to 5). Do not probe hardware, OS or shell details, installed tool versions, or unrelated environment state unless the task depends on them. If uncertainty remains after reasonable inspection, state the assumption in the plan instead of continuing to probe.

Once enough context is available, output the plan immediately. Return only the plan: no investigation notes, tool results, or running commentary.

## Output Format

For one approach, use exactly:

## Plan
- **Approach**: one-line summary of the strategy
- **Steps**: numbered list of actions in execution order
- **Files**: files that will be created or modified, one per line with a brief note
- **Commands**: shell commands that will be run, one per line
- **Risks**: potential issues, edge cases, or limitations

For materially different alternatives, use:

## Proposal 1: Brief Title
- **Approach**: ...
- **Steps**: ...
- **Files**: ...
- **Commands**: ...
- **Risks**: ...

## Proposal 2: Brief Title
- **Approach**: ...
- **Steps**: ...
- **Files**: ...
- **Commands**: ...
- **Risks**: ...

Include every field; write None when no files or commands apply. Each proposal must be self-contained and independently actionable. Proposal headings must start with exactly "## Proposal N: " at heading level 2; do not nest them under another heading.

The user will approve, revise, or reject the plan before execution.`

	ShellClassifier = `Analyze this shell command for review.
For process_run, the command is a JSON description of a direct process invocation; program and args are literal and have no shell expansion.
Return only strict JSON. Do not include <think> tags, Markdown fences, prose, or explanations.
Use exactly this shape:
{"effect":"read|write|unknown","affected_dirs":["/path/or/relative/dir"],"reason":"short reason"}

Set affected_dirs to the directories that may be read, written, deleted, modified, or used as the command's working context. If none are affected or unknown, use an empty array.
Every affected_dirs entry must be a concrete literal directory. Never return shell variables, PowerShell automatic variables, command substitutions, placeholders, or prose as a directory; use an empty array when the target is resolved only at runtime.
Set effect to "read" only when the command is read-only, "write" when it writes or may write persistent state, and "unknown" when unsure.
Example: ls -la /path/to/project => {"effect":"read","affected_dirs":["/path/to/project"],"reason":"lists directory contents only"}.`
)

type Definition struct {
	Name         string
	Description  string
	Default      string
	Configurable bool
}

func Builtin() []Definition {
	return []Definition{
		{Name: KeyIdentity, Description: "Base Mods identity and behavior instructions.", Default: Identity, Configurable: true},
		{Name: KeyToolSelection, Description: "Capability-filtered guidance for choosing native filesystem and shell tools.", Default: ToolSelection, Configurable: true},
		{Name: KeyPlan, Description: "System prompt used while drafting an approval plan.", Default: Plan, Configurable: true},
		{Name: KeyShellClassifier, Description: "Classifier prompt used to decide whether shell commands need review.", Default: ShellClassifier, Configurable: true},
		{Name: KeyMinimal, Description: "System prompt added by --minimal.", Default: Minimal},
		{Name: KeyFormatMarkdown, Description: "Formatting prompt used by --format --format-as markdown.", Default: MarkdownFormat},
		{Name: KeyFormatJSON, Description: "Formatting prompt used by --format --format-as json.", Default: JSONFormat},
		{Name: KeySafeWorkspaceTemplate, Description: "Template for the safe temporary workspace system prompt.", Default: SafeWorkspaceTemplate},
		{Name: KeyApprovedPlanTemplate, Description: "Template inserted after the user approves a plan.", Default: ApprovedPlanTemplate},
	}
}

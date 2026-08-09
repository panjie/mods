package prompts

const (
	KeyIdentity        = "identity"
	KeyToolSelection   = "tool-selection"
	KeyShellClassifier = "shell-classifier"
)

const (
	KeyMinimal               = "minimal"
	KeyFormatMarkdown        = "format.markdown"
	KeyFormatJSON            = "format.json"
	KeySafeWorkspaceTemplate = "safe-workspace-template"
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

	ToolSelectionProcess = `- Use process_run for one executable, including git, tests, builds, package managers, and installers, with literal args/cwd; Windows .bat/.cmd require powershell_run. Never use it for shell -c/-Command. Inspect results; use runtime_info for unknown availability.`

	ToolSelectionShellPOSIX = `- Use shell_run for commands that require POSIX shell syntax: pipelines, redirection, expansion, globs, and builtins. It runs in reported cwd; do not prefix cd. Prefer portable sh, print inspections directly, and pass file lists through NUL pipelines rather than command substitution (git ls-files -z | xargs -0 ...).`

	ToolSelectionShellPOSIXFallback = `- Use shell_run for executable invocations and POSIX shell features. Keep each call single-purpose. It runs in the reported cwd; do not prefix cd. Prefer portable sh, print inspection results instead of writing temporary files, and pass file lists through NUL-delimited pipelines rather than command substitution (git ls-files -z | xargs -0 ...).`

	PowerShellIntentGuidance = `Prefer short, single-purpose commands. Separate discovery, path inspection, mutation, and verification. Resolve runtime writes such as $PROFILE read-only, then use the literal absolute path. Do not change execution policy or unrelated settings unless requested. Avoid decorative formatting and dynamic or encoded commands; keep necessary pipelines intact.`

	ToolSelectionShellWindows = `- Use powershell_run only for cmdlets, object pipelines, or runtime variables. Keep cwd, pass only the command, and match the reported PowerShell host. Prefer native cmdlets (Get-ChildItem, Select-String, Where-Object, Test-Path, Get-Content, Measure-Object) over POSIX utilities. ` + PowerShellIntentGuidance + ` Return inspection output directly; do not write files merely to see results.`

	ToolSelectionShellWindowsFallback = `- Use powershell_run for executable invocations and commands that require PowerShell cmdlets, object pipelines, runtime variables, or other PowerShell syntax. Commands run in cwd; do not prefix Set-Location, cd, or Push-Location. Use syntax compatible with the reported PowerShell host and pass only the command without powershell/pwsh -Command. Prefer native cmdlets such as Get-ChildItem and Select-String over POSIX-only utilities. ` + PowerShellIntentGuidance + ` Return inspection output directly; do not write temporary files merely to see results.`

	// ToolSelection is the complete normal-mode reference shown by
	// --list-prompts. Runtime requests select only the capability blocks for
	// tools that are actually registered.
	ToolSelection = ToolSelectionGeneral + "\n" +
		ToolSelectionFilesystem + "\n" +
		ToolSelectionProcess + "\n" +
		ToolSelectionShellPOSIX + "\n" +
		ToolSelectionShellWindows

	SafeWorkspaceTemplate = "Safe temporary workspace: {safe_workspace}. File write and shell operations within this directory and its subdirectories are auto-approved without user review. Prefer this directory for temporary scripts, intermediate files, and experimental writes."

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
		{Name: KeyShellClassifier, Description: "Classifier prompt used to decide whether shell commands need review.", Default: ShellClassifier, Configurable: true},
		{Name: KeyMinimal, Description: "System prompt added by --minimal.", Default: Minimal},
		{Name: KeyFormatMarkdown, Description: "Formatting prompt used by --format --format-as markdown.", Default: MarkdownFormat},
		{Name: KeyFormatJSON, Description: "Formatting prompt used by --format --format-as json.", Default: JSONFormat},
		{Name: KeySafeWorkspaceTemplate, Description: "Template for the safe temporary workspace system prompt.", Default: SafeWorkspaceTemplate},
	}
}

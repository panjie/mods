package prompts

const (
	KeyIdentity               = "identity"
	KeyToolSelection          = "tool-selection"
	KeyShellClassifier        = "shell-classifier"
	KeyPromptIntentClassifier = "prompt-intent-classifier"
	KeyWriteScopeClassifier   = "write-scope-classifier"
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

	ToolSelectionProcess = `- Use process_run for one executable, including git, tests, builds, package managers, installers, and interpreters with script or expression arguments (emacs --eval, python -c, node -e), with literal args/cwd. Pass each argument as one argv item even when it contains quotes or parentheses; stdout and stderr return separately, so do not add 2>&1. Windows .bat/.cmd require powershell_run. Never use it for shell -c/-Command. Inspect results; use runtime_info for unknown availability.`

	ToolSelectionShellPOSIX = `- Use shell_run for commands that require POSIX shell syntax: pipelines, redirection, expansion, globs, and builtins. It runs in reported cwd; do not prefix cd. Prefer portable sh, print inspections directly, and pass file lists through NUL pipelines rather than command substitution (git ls-files -z | xargs -0 ...).`

	ToolSelectionShellPOSIXFallback = `- Use shell_run for executable invocations and POSIX shell features. Keep each call single-purpose. It runs in the reported cwd; do not prefix cd. Prefer portable sh, print inspection results instead of writing temporary files, and pass file lists through NUL-delimited pipelines rather than command substitution (git ls-files -z | xargs -0 ...).`

	PowerShellIntentGuidance = `Prefer short, single-purpose commands. Separate discovery, path inspection, mutation, and verification. Resolve runtime writes such as $PROFILE read-only, then use the literal absolute path. Do not change execution policy or unrelated settings unless requested. Avoid decorative formatting and dynamic or encoded commands; keep necessary pipelines intact.`

	ToolSelectionShellWindows = `- Use powershell_run, the only Windows shell tool, for cmdlets, object pipelines, runtime variables, and shell syntax. Keep cwd, pass only the command, and match the reported PowerShell host. Prefer native cmdlets (Get-ChildItem, Select-String, Where-Object, Test-Path, Get-Content, Measure-Object) over POSIX utilities. ` + PowerShellIntentGuidance + ` Return inspection output directly; do not write files merely to see results.`

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
The user message supplies authoritative Workspace and Home values. Use them exactly when resolving paths; an unquoted current-user ~ resolves to Home. Never guess a home directory such as /home/user.
Set effect to "read" only when the command is read-only, "write" when it writes or may write persistent state, and "unknown" when unsure.
Example: ls -la /path/to/project => {"effect":"read","affected_dirs":["/path/to/project"],"reason":"lists directory contents only"}.`

	PromptIntentClassifier = `Classify which capabilities the user's message explicitly requests the assistant to exercise.
Return only strict JSON. Do not include <think> tags, Markdown fences, prose, or explanations.
Use exactly this shape:
{"intents":["workspace-edit","global-read"]}

Choose intents only from this closed list:
- "workspace-edit": the user asks to change files in the workspace — create, edit, delete, rename, refactor, commit, build, install, or format (for example "fix this bug", "commit the changes", "install the dependencies", "refactor the parser").
- "global-read": the user asks to read or inspect the filesystem beyond the workspace — system files, configuration outside the workspace, or anything requiring global read access (for example "show me /etc/hosts", "check my global npm config").
Rules:
- Include an intent only when the user's own words request that capability now; do not infer it from general discussion, questions, or read-only exploration.
- Multiple intents are allowed in one reply.
- When nothing matches or you are unsure, return an empty array.
- Never invent labels outside the closed list.
Example: "commit the local changes" => {"intents":["workspace-edit"]}
Example: "what does my global .gitconfig contain" => {"intents":["global-read"]}
Example: "explain what this Makefile does" => {"intents":[]}`

	WriteScopeClassifier = `Classify where a command's local filesystem write effect lands.
For process_run, the command is a JSON description of a direct process invocation; program and args are literal and have no shell expansion.
Return only strict JSON. Do not include <think> tags, Markdown fences, prose, or explanations.
Use exactly this shape:
{"scopes":["workspace","external","unknown"]}

Choose scopes only from this closed list (any subset, possibly empty):
- "workspace": the command writes local files only within the Workspace directory (including the repository's .git, node_modules, build artifacts, and cwd-relative outputs).
- "external": the command writes local files outside the Workspace (for example system config under /etc, a global config, or an absolute path outside the Workspace).
- "unknown": the local write target cannot be determined.
Rules:
- Report only the LOCAL filesystem write effect. Remote/network effects (pushing, publishing, uploading, deploying) are not local writes and must not add any scope.
- Return an empty array only when you are confident the command performs no local filesystem write at all (a purely remote or network operation).
- When unsure whether a local write occurs or where it lands, include "unknown"; never return an empty array merely because you cannot tell.
- Include every applicable scope; a command may touch several at once.
- Never invent labels outside the closed list.
The user message supplies authoritative Workspace and Home values.
Example: git add -A => {"scopes":["workspace"]}
Example: git push origin main => {"scopes":["workspace"]}
Example: curl -X POST https://api.example.com => {"scopes":[]}
Example: write a file to /etc/config => {"scopes":["external"]}`
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
		{Name: KeyPromptIntentClassifier, Description: "Classifier prompt that maps the user's message onto closed prompt-intent labels for pre-authorization.", Default: PromptIntentClassifier, Configurable: true},
		{Name: KeyWriteScopeClassifier, Description: "Classifier prompt that scopes a write command's local filesystem effect for prompt-intent approval.", Default: WriteScopeClassifier, Configurable: true},
		{Name: KeyMinimal, Description: "System prompt added by --minimal.", Default: Minimal},
		{Name: KeyFormatMarkdown, Description: "Formatting prompt used by --format --format-as markdown.", Default: MarkdownFormat},
		{Name: KeyFormatJSON, Description: "Formatting prompt used by --format --format-as json.", Default: JSONFormat},
		{Name: KeySafeWorkspaceTemplate, Description: "Template for the safe temporary workspace system prompt.", Default: SafeWorkspaceTemplate},
	}
}

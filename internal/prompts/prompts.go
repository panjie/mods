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
	ToolSelection  = "Tool selection. Priority order: 1. Use fs_* tools for files inside the configured workspace; they are auto-approved for reads and reviewed only for writes. 2. fs_* tools may also access files outside the workspace (Downloads, Desktop, system temp, etc.); such access triggers an approval prompt. Prefer workspace-local paths to minimize interruptions. 3. On Windows, use shell_run for cmd.exe builtins such as dir, type, and echo; use powershell_run for PowerShell pipelines, variables, filtering, counting, or querying. Pass only the PowerShell command, without powershell, powershell.exe, pwsh, or -Command prefixes. 4. Minimize tool calls by using one well-formed command instead of repeated small retries. 5. Return command output directly; avoid redirection, Out-File, Set-Content, or temporary scripts just to inspect results. Shell output redirection (>, >>) writes files and triggers review. 6. For multi-step work that genuinely needs intermediate files, write them inside the configured workspace so fs_read_file can inspect them without shell review. 7. Mutating and destructive shell commands (delete, move, rename, overwrite) are automatically routed through mods' review step — when the user requests such an action, attempt it directly rather than asking for permission first."

	SafeWorkspaceTemplate = "Safe temporary workspace: {safe_workspace}. File write and shell operations within this directory and its subdirectories are auto-approved without user review. Prefer this directory for temporary scripts, intermediate files, and experimental writes."

	ApprovedPlanTemplate = "The user has approved the following plan for execution:\n\n{approved_plan}\n\nFollow this approved plan during execution. If new information requires changing it, explain the reason before deviating."

	Plan = `You are in PLAN mode. Before executing anything, you must first create a detailed, step-by-step plan for the user to review.

CRITICAL — PLANNING PHASE ONLY. You are NOT authorized to:

- Write any scripts or programs (Python, shell, JS, etc.) — even "temporary" or "experimental" ones
- Create or modify any files anywhere (workspace, /tmp, or safe workspace)
- Run any self-written code or script
- Execute commands that produce the task's final output

Investigation means READING, not BUILDING. If you catch yourself writing a script, STOP — that script belongs in the plan, not in your current tool calls.

Valid investigation means read-only inspection. Use platform-appropriate read-only commands for listing directories, reading files, searching text, and checking metadata; do not redirect output to files. Built-in read-only tools such as fs_list_dir, fs_read_file, fs_stat, and fs_search are allowed.

When you have enough context, output the plan IMMEDIATELY. Do not over-investigate. Do not include investigation notes, tool call results, or running commentary — just the plan itself.

Investigate only as much as needed to write an accurate plan. Before running any command, confirm it is directly relevant to the user's request; if it is not, skip it. Do NOT probe hardware (CPU, RAM, GPU), OS version, shell environment, installed tool versions, or system specs unless the task explicitly depends on them — that information is almost never needed. Aim for a few targeted reads (around 3 to 5); if you still cannot decide after that, state your assumptions explicitly in the plan and proceed, rather than continuing to probe. The goal is a sound plan, not exhaustive investigation.

## Output Format

### Single approach — use this heading and structure:

## Plan
- **Approach**: one-line summary of the strategy
- **Steps**: numbered list of actions in execution order
- **Files**: files that will be created or modified, one per line with a brief note
- **Commands**: shell commands that will be run, one per line
- **Risks**: potential issues, edge cases, or limitations

### Multiple approaches — use this structure for each:

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

Each proposal must be self-contained and independently actionable.

Each proposal heading MUST begin with exactly two hash characters followed by a space (for example, "## Proposal 1: Title"). Do not use three or more hash characters and do not nest proposals under another heading; the proposal parser recognizes proposals only at this exact heading level.

The user will review your plan and approve or deny it before execution begins.`

	ShellClassifier = `Analyze this shell command for review.
Return only strict JSON. Do not include <think> tags, Markdown fences, prose, or explanations.
Use exactly this shape:
{"needs_review":true,"affected_dirs":["/path/or/relative/dir"],"reason":"short reason"}

Set needs_review to true if the command creates, deletes, modifies, or may modify files, directories, system settings, or persistent state. If unsure, set needs_review to true.
Set affected_dirs to the directories that may be written, deleted, or modified. If none are affected or unknown, use an empty array.
Example: ls -la /path/to/project => {"needs_review":false,"affected_dirs":[],"reason":"lists directory contents only"}.`
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
		{Name: KeyToolSelection, Description: "Guidance for choosing native filesystem and shell tools.", Default: ToolSelection, Configurable: true},
		{Name: KeyPlan, Description: "System prompt used while drafting an approval plan.", Default: Plan, Configurable: true},
		{Name: KeyShellClassifier, Description: "Classifier prompt used to decide whether shell commands need review.", Default: ShellClassifier, Configurable: true},
		{Name: KeyMinimal, Description: "System prompt added by --minimal.", Default: Minimal},
		{Name: KeyFormatMarkdown, Description: "Formatting prompt used by --format --format-as markdown.", Default: MarkdownFormat},
		{Name: KeyFormatJSON, Description: "Formatting prompt used by --format --format-as json.", Default: JSONFormat},
		{Name: KeySafeWorkspaceTemplate, Description: "Template for the safe temporary workspace system prompt.", Default: SafeWorkspaceTemplate},
		{Name: KeyApprovedPlanTemplate, Description: "Template inserted after the user approves a plan.", Default: ApprovedPlanTemplate},
	}
}

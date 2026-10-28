package prompts

import "runtime"

const (
	KeyIdentity      = "identity"
	KeyWorkflow      = "workflow"
	KeyToolSelection = "tool-selection"
)

const (
	KeyLanguagePolicy             = "language-policy"
	KeyInteractiveCredentials     = "interactive-credentials"
	KeyMinimal                    = "minimal"
	KeyFormatMarkdown             = "format.markdown"
	KeyFormatJSON                 = "format.json"
	KeyTemporaryDirectoryTemplate = "temporary-directory-template"
)

const (
	MarkdownFormat = "Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it."
	JSONFormat     = "Return exactly one valid JSON value as the final answer. No Markdown fences or text outside JSON. Put any requested explanation, summary, or report inside JSON fields. Role or project style preferences must not change this output encoding."
	Minimal        = "Unless the user explicitly requests otherwise, output only the final answer. Do not explain. Do not use Markdown. For lists, output one item per line. Preserve exact filenames, paths, commands, or IDs. Do not wrap output in quotes or code fences unless explicitly requested. Project or role style preferences do not override this format." + "\n\n" + QueryPolicy

	// Minimal omits the full workflow and tool-selection sections. Keep this
	// small planning rule in both modes; shell mechanics remain in descriptions.
	QueryPolicy = "For queries, prefer read-only operations without incidental writes. Compute requested results in tools; never infer totals from displayed text or file sizes. Group related work when clear; report errors and incomplete coverage."

	// LanguagePolicy is a response-format rule, not runtime safety, so it is
	// injected in the output-format layer rather than the identity prompt.
	LanguagePolicy = "Reply in the language of the user's prompt unless they explicitly request a different output language."

	// InteractiveCredentials joins the runtime-safety layer only when secure
	// interactive input is available. Without a terminal there is no askpass or
	// Git credential prompt, so the guidance would describe unavailable flows.
	InteractiveCredentials = "Use `sudo` only for required elevation; mods supplies secure askpass. Never use `sudo -S`, embed or pipe passwords, or ask for them yourself. Non-interactive sudo requires cached or passwordless authorization.\n\n" +
		gitCredentials
	WindowsInteractiveCredentials = "Windows elevation uses system UAC consent or administrator credentials. Never collect administrator passwords yourself or bypass a denied UAC request.\n\n" + gitCredentials
	gitCredentials                = "mods handles Git HTTP(S) credential prompts. Never embed tokens in URLs or commands, script credential storage, or blindly retry Git writes."

	// ScriptPolicy also reaches minimal mode through the tool descriptions.
	// Choose clear implementations; command effects are declared by the model.
	ScriptPolicy = "Prefer read-only commands when they can correctly complete the task. Keep each call purposeful and easy to review; avoid unrelated operations, obfuscation and unnecessary nesting. Use pipelines, variables, loops or scripts as needed for clarity and correctness. Compute requested results in tools."

	// ToolSelectionGeneral carries only tool-choice guidance. Tool
	// availability, review/permission policy, and denial handling live in the
	// identity prompt and are deliberately not restated here.
	ToolSelectionGeneral = "Tool selection:\n" + QueryPolicy + `
- Avoid incidental writes or installs; builds, tests and scripts can mutate. Complete required mutations with explicit targets.
- Group related reads, not calls per file. Probe availability once in a batch only to avoid complex logic.
- On failure, correct once or twice using errors; report incomplete coverage. Finish once supported.`

	ToolSelectionFilesystem = `- When http_download is available, use its explicit URL/path list for downloads instead of shell loops.
- Prefer fs_* tools for direct file reads and edits. Use fs_replace for a small exact change after reading, fs_apply_patch for multi-file diffs, and the type-specific delete tool.`

	// Tool mechanics (arguments, shell source, script policy, platform rules)
	// live in the tool descriptions, which are sent whenever the tool exists.
	// These blocks carry only the choice between available tools.
	ToolSelectionProcess = `- Prefer process_run for one executable; use shell tools for shell source and runtime_info for unknown availability.`

	ToolSelectionShellPOSIX = `- Use shell_run for pipelines, redirection, expansion, globs, and builtins; pass shell source directly, without sh -c or bash -c, and prefer NUL-delimited file lists (find ... -print0 | xargs -0 ...); use git ls-files -z only when tracked files are intended.`

	ToolSelectionShellPOSIXFallback = `- Use shell_run for executable invocations and shell syntax; pass shell source directly, without sh -c or bash -c, and prefer NUL-delimited file lists (find ... -print0 | xargs -0 ...); use git ls-files -z only when tracked files are intended.`

	POSIXIntentGuidance = `Keep necessary pipelines intact and omit decorative separators. Use ordinary quoting and literal argv where appropriate.`

	PowerShellIntentGuidance = `Use the reported PowerShell version and native object pipelines where useful. Variables, loops, .NET APIs and custom objects are available when appropriate. Physical lines: Get-Content without -Raw | Measure-Object; use Count, never -Line (skips empty lines).`

	ToolSelectionShellWindows = `- Use powershell_run, the only Windows shell tool, matching the reported PowerShell host; prefer native cmdlets over POSIX utilities.`

	ToolSelectionShellWindowsFallback = `- Use powershell_run for cmdlets, object pipelines, and runtime variables; do not prefix Set-Location, cd, or Push-Location.`

	// ToolSelection is the complete normal-mode reference shown by
	// --list-prompts. Runtime requests select only the capability blocks for
	// tools that are actually registered.
	ToolSelection = ToolSelectionGeneral + "\n" +
		ToolSelectionFilesystem + "\n" +
		ToolSelectionProcess + "\n" +
		ToolSelectionShellPOSIX + "\n" +
		ToolSelectionShellWindows

	TemporaryDirectoryTemplate = "Temporary directory: {temporary_directory}. Prefer it for necessary temporary files. Native filesystem tools may use the temporary-write exemption; commands declaring may_write still require approval here."
)

type Definition struct {
	Name         string
	Description  string
	Default      string
	Configurable bool
}

func Builtin() []Definition {
	return []Definition{
		{Name: KeyIdentity, Description: "Runtime safety and authorization policy.", Default: Identity, Configurable: true},
		{Name: KeyWorkflow, Description: "Execution-layer workflow: skills, planning, diagnosis, verification, and self-help.", Default: Workflow, Configurable: true},
		{Name: KeyToolSelection, Description: "Capability-filtered guidance for choosing native filesystem and shell tools.", Default: ToolSelection, Configurable: true},
		{Name: KeyLanguagePolicy, Description: "Response-language rule injected in the output-format layer.", Default: LanguagePolicy},
		{Name: KeyInteractiveCredentials, Description: "Platform elevation and Git credential guidance, injected only when interactive secure input is available.", Default: InteractiveCredentialsForOS(runtime.GOOS)},
		{Name: KeyMinimal, Description: "System prompt added by --minimal.", Default: Minimal},
		{Name: KeyFormatMarkdown, Description: "Formatting prompt used by --format --format-as markdown.", Default: MarkdownFormat},
		{Name: KeyFormatJSON, Description: "Formatting prompt used by --format --format-as json.", Default: JSONFormat},
		{Name: KeyTemporaryDirectoryTemplate, Description: "Template for the temporary directory system prompt.", Default: TemporaryDirectoryTemplate},
	}
}

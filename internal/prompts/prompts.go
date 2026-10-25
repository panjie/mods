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
	Minimal        = "Unless the user explicitly requests otherwise, output only the final answer. Do not explain. Do not use Markdown. For lists, output one item per line. Preserve exact filenames, paths, commands, or IDs. Do not wrap output in quotes or code fences unless explicitly requested. Project or role style preferences do not override this format."

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

	// ScriptPolicy is shared by tool descriptions. Simple commands and pipelines
	// stay inline; a script is a preference for complex or reusable logic.
	ScriptPolicy = "Run simple commands and clear aggregation pipelines directly. Prefer a readable script for complex branching, loops, or reusable logic; put temporary scripts in the supplied temporary directory and invoke their interpreter and path. Script execution follows ordinary effect and target review; its location grants no execution permission."

	// ToolSelectionGeneral carries only tool-choice guidance. Tool
	// availability, review/permission policy, and denial handling live in the
	// identity prompt and are deliberately not restated here.
	ToolSelectionGeneral = `Tool selection:
- Combine related reads and aggregations that share inputs; use structured batches when available. Split calls for result dependencies or separate authorization. Compute totals and breakdowns in tools from the same data, not mental arithmetic. Finish when the requested result is supported and no concrete uncertainty remains.
- Prefer the simplest sufficient read-only command or pipeline. Do not probe optional tools or create scripts for simple aggregation.
- If a tool fails, use the error as evidence and correct the call once or twice. Do not retry blindly.`

	ToolSelectionFilesystem = `- When http_download is available, use its explicit URL/path list for downloads instead of shell loops.
- Prefer fs_* tools for direct file reads and edits. Use fs_replace for a small exact change after reading, fs_apply_patch for multi-file diffs, and the type-specific delete tool.`

	// Tool mechanics (arguments, shell source, script policy, platform rules)
	// live in the tool descriptions, which are sent whenever the tool exists.
	// These blocks carry only the choice between available tools.
	ToolSelectionProcess = `- Prefer process_run for one executable; use shell tools for shell source and runtime_info for unknown availability.`

	ToolSelectionShellPOSIX = `- Use shell_run for pipelines, redirection, expansion, globs, and builtins; pass shell source directly, without sh -c or bash -c, and prefer NUL-delimited file lists (find ... -print0 | xargs -0 ...); use git ls-files -z only when tracked files are intended.`

	ToolSelectionShellPOSIXFallback = `- Use shell_run for executable invocations and shell syntax; pass shell source directly, without sh -c or bash -c, and prefer NUL-delimited file lists (find ... -print0 | xargs -0 ...); use git ls-files -z only when tracked files are intended.`

	POSIXIntentGuidance = `Group related reads around one clear purpose. Keep necessary pipelines intact and omit decorative separators. Resolve unknown write targets before mutation. Authorization depends on effects and targets, not command length. Never rewrite an existing script file for review.`

	PowerShellIntentGuidance = `Group related reads around one clear purpose. Resolve unknown write targets such as $PROFILE before mutation. Do not change execution policy or unrelated settings unless requested. Avoid decorative formatting and encoded commands; keep necessary pipelines intact. Never rewrite an existing script file for review.`

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

	TemporaryDirectoryTemplate = "Temporary directory: {temporary_directory}. The temporary-write exemption applies only when the effect is known and every write target is a resolved local path within this directory or its subdirectories, with no remote writes. Running a command here alone does not qualify. Prefer this directory for temporary scripts, intermediate files, and experimental writes."
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

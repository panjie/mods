# Tool Details

Mods ships its built-in tools inside the binary. Filesystem tools auto-activate
when a request needs them; shell tools are enabled by default. Run
`mods --list-tools` for the catalogue available in your build, including any MCP
tools.

Filesystem paths resolve from the process current working directory. Shell and
process tools start there by default and accept a per-call `cwd` argument. The
status line shows what Mods is doing between tool calls ("Reading file: ...",
"Running command: ...", "Searching web: ..."). `--hide-tool-status` hides it and
the compact one-line record each completed call leaves in normal output.

## Command Reviewability

Command complexity is recorded only in debug diagnostics. It never returns a
failed tool result or asks the model to resubmit. Every command goes directly to
ordinary effect and target approval, including in minimal mode. Unknown effects
still require the existing approval checks, and denial prevents execution.

Run simple commands and clear aggregation pipelines directly. Prefer a readable
script for complex branching, loops, or reusable logic. Reuse collected data for
subtotals; split calls when a result must be inspected before the next step or
authorization needs to be separate. Existing scripts such as
`bash scripts/build.sh`, `python tools/check.py`, and `pwsh -File build.ps1` run
unchanged once authorized. New multi-step scripts can use the supplied temporary
directory; that location grants no permission for their execution effects.
Explicit `--review-mode never` bypasses review.

Long command reviews use Up/Down to page; approval becomes available after all
pages have been displayed. Review is an approval policy, not an execution
sandbox.

## http_download

`http_download` accepts up to 32 URL/path pairs (16 MiB per file), stops on the
first failure, and reports completed/failed/skipped entries. It requires
existing parent directories and preserves existing files unless `overwrite` is
explicit. Downloads use the web-search private-network opt-in and never execute
content. Publishing uses a same-directory rename for overwrite or a hard link
for no-overwrite; filesystems without the required operation fail without
falling back to a partial destination write. Batch completion is not
transactional.

## Git Authentication

When Git needs an HTTP(S) username or password/token during a tool call, Mods
can prompt in the terminal and continue the same command. Existing credential
helpers, `GIT_ASKPASS`, `core.askPass`, and `SSH_ASKPASS` retain priority. Mods
installs its bridge only as the last askpass fallback; it does not change Git
configuration or add an authentication tool.

Passwords are masked and passed directly to Git through a local IPC connection,
without going through the model. Mods does not save credentials, though your
configured Git credential helper may do so. Prefer HTTPS and your existing
credential manager for persistent authentication. Windows requires Git for
Windows, including its bundled shell.

Input counts toward the command timeout (60 seconds by default). Escape cancels
authentication; timeout or task cancellation also closes the prompt. Mods does
not automatically rerun the command. Raw/minimal mode and non-interactive
sessions cannot open these prompts: configure credentials beforehand. SSH private-key unlocking, host-key confirmation, and interactive
`gh auth login` are not handled by this bridge.

## Debug Output

Use `-D` or `--debug` to write a human-readable execution trace to stderr. The
trace groups startup settings, each user turn, model rounds, tool calls,
approval decisions, results, timing, token usage, and the final turn summary.
Provider call IDs and `call N/total` labels connect every tool request to its
result, including validation failures, denied reviews, cancellations, and
non-zero command exits.

The request's `prompt bytes` line measures the rendered system text and complete
provider-neutral tool definitions, with tool descriptions and schema JSON shown
as breakdowns. Schema property descriptions count too. The total is system text
plus tool-definition JSON; do not add the breakdowns again. These byte counts
exclude conversation content and media and are not token estimates or exact
provider wire sizes. The separate request-size line includes message history.

Tool arguments are pretty-printed as JSON up to 4 KiB. Tool results are shown up
to 16 KiB. Larger payloads retain both the beginning and end and report the
number of omitted bytes. Empty results are shown explicitly as `<empty>`. Known
protected credentials are redacted before formatting or truncation, and
resolved credential values are never sent to the debug renderer. Terminal
output uses color; redirected debug output contains the same labels without ANSI
styling.

Use `--show-token-usage` (or `-s`) to print the input, output, and total token
count for an interaction to stderr without mixing it into the model response on
stdout.

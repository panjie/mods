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

Compound and opaque command text gets advisory simplification feedback before
approval. Mods nudges the model at most twice per request toward shapes whose
effects and targets can be reviewed, and `--minimal` keeps that advice. The
advice never blocks anything. Once the two nudges are used, or whenever the
command is already reviewable, the call goes to ordinary approval and you
decide; no command is ever rejected on structural grounds. Pipelines that
represent one operation are left intact.

Commands whose payload is a pre-written script file are exempt from the nudges
entirely: `bash scripts/build.sh`, `python tools/check.py`, and `pwsh -File
build.ps1` run exactly as written, because a skill's own scripts must not be
rewritten to satisfy review. Ad-hoc inline source (`-c`, `-e`, `-Command`,
encoded payloads) still gets the advice. There is no general script execution
tool: put multi-step work in a script file instead of hiding it in interpreter
flags or encoded arguments. Explicit `--review-mode never` bypasses review.

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
not automatically rerun the command. Raw/minimal mode, non-interactive sessions,
and write-target discovery cannot open these prompts: configure credentials
beforehand. SSH private-key unlocking, host-key confirmation, and interactive
`gh auth login` are not handled by this bridge.

## Debug Output

Use `-D` or `--debug` to write a human-readable execution trace to stderr. The
trace groups startup settings, each user turn, model rounds, tool calls,
approval decisions, results, timing, token usage, and the final turn summary.
Provider call IDs and `call N/total` labels connect every tool request to its
result, including validation failures, denied reviews, cancellations, and
non-zero command exits.

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

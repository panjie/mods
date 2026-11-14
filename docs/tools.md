# Tool Details

Mods ships its built-in tools inside the binary. Filesystem tools auto-activate
when a request needs them; shell tools are enabled by default. Run
`mods --list-tools` for the catalogue available in your build, including any MCP
tools.

Filesystem paths resolve from the process current working directory. Shell and
process tools start there by default and accept a per-call `cwd` argument. The
status line shows what Mods is doing between tool calls ("read ...",
"shell ...", "search web ..."). `--hide-tool-status` hides it and
the compact one-line record each completed call leaves in normal output.

## Command Declarations

`shell_run`, `powershell_run`, and `process_run` require `effect: read_only` or
`effect: may_write`. The declaration covers the whole call, including subprocesses,
pipelines and scripts. `may_write` includes uncertain effects and mixed reads/writes.
Missing, null, invalid or incorrectly typed values are argument errors.
Each call also requires a nonblank `purpose` explaining the operation and its
meaningful effects in the user's language. Purpose is display text and does not
affect authorization.

In `auto` and `always`, `read_only` executes immediately; `may_write` asks for a
one-call approval. Saved directory/remote rules and temporary paths do not exempt
commands. `never` disables review but does not bypass argument validation or UAC.
In `auto` and `always`, `may_write` calls without an interactive review channel
return approval unavailable.

Mods trusts the model's declaration and does not independently analyze effects.
The model can misclassify a mutation as a read. There is no command analyzer,
PowerShell parser bridge, complexity gate, automatic reorganization or script
content preview/digest. Existing scripts execute by interpreter and path.

Tool descriptions in normal and minimal mode instruct models to prefer reads
when they can correctly finish the task, generate clear reviewable commands, and
label the final call honestly. Variables, loops, .NET calls and scripts are allowed;
necessary mutations should use `may_write`, not be omitted to avoid approval.

The review displays the purpose, normalized working directory, and complete
command: quoted executable/arguments for `process_run`, or shell source with line
numbers for multi-line commands. Y approves once, N denies, and Ctrl+C/Esc
cancels the task. F selects **Full control (this session)**: approve this operation
and disable subsequent tool review, including native and MCP writes, for the
current invocation and all subsequent `--chat` turns. This is an in-memory
switch to `never`, not a saved permission rule or configuration change. Restarting
Mods (including `--continue`) restores the configured review mode. Argument
validation, filesystem execution checks, and OS credential/UAC prompts still apply.
Left/Right moves the highlighted choice; Enter confirms it.
Up/Down browses long commands without disabling approval. Source is escaped and
known secrets are redacted for display. Approval uses the saved call arguments;
there is no effect reanalysis or script-content reapproval.

Native file, download, and MCP review prompts offer the same Y/N/F/cancel choices.
`Allow for session` and saved target grants are retired; historical approval rules
never bypass review. Native file tools and MCP tools retain their own capabilities
and target policy.
An `effect` argument cannot relabel a native file write as a read. Downloads retain
structured destination review and their existing page-reading requirement.

## web_search

Enable `web_search` with `web-search: true` or `--web-search`. It uses the
configured `web-search-provider` (Tavily, DeepSeek, or a custom search URL) and
its search API key. The selected model and endpoint do not replace this service
with provider-hosted search. Disabling web search removes the tool.

## web_fetch

By default (`web-fetch-provider: local`), `web_fetch` reads HTTP(S) HTML, plain text and Markdown without a browser,
search-service key, JavaScript execution or local file writes. It is available
independently of `--web-search` and filesystem/shell settings. Ordinary webpage
and documentation queries prefer it over browser skills and commands; failures
should be reported or followed by web search, not automatic browser invocation.
Browsers are reserved for explicit interaction tasks.

HTML extraction prefers `main` or `article`, falling back to the body, removes
navigation and scripts, and preserves code-block whitespace. It does not render
CSS or JavaScript and cannot read authenticated browser sessions. Fragment URLs
are accepted but return the whole document. Results contain the final URL,
title, text, `total_characters`, `next_offset` and `done`. Use `offset` and `limit`
(default 12000, maximum 20000 Unicode characters) to page long documents. Each
call fetches again, so changing pages may shift offsets. Web content is untrusted
source material, not instructions.

Responses and decoded text are limited to 4 MiB, with a 30-second request timeout
and at most ten redirects. Only HTTP(S) URLs without embedded credentials are
accepted; HTTPS downgrades are refused. DNS and redirect destinations use the
same private-network protection as web search and downloads, with the existing
`MODS_WEB_SEARCH_ALLOW_PRIVATE=1` opt-in.

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

## Installed skill directories

Use `mods --skills-dirs DIR` to select installed skill directories. Repeat the
flag to supply multiple directories; later directories override earlier skills
with the same name. Use `mods --skills-dirs` without a directory to print the
effective directories.

The default is `~/.agents/skills`. Portable mode also includes a `skills`
directory next to the executable. Use `mods --list-skills` to list the installed
skills from the effective directories.

When a task matches an installed skill's description, or the user names a skill,
the model is instructed to load it before related investigation or changes.
This includes read-only questions covered by the skill. Limited discovery to
establish applicability is allowed; already loaded skills need not be reloaded.

The prompt catalog preserves complete descriptions within a 4096-byte total
budget. Entries that do not fit are omitted with a notice to use `search_skills`.
Search matches full descriptions and returns complete descriptions within its
own 4096-byte output budget. An oversized description is explicitly marked as
omitted, with instructions to load that skill to check applicability. These are
model instructions, not a runtime gate on other tools.


## DeepSeek document and speech tools

These opt-in standalone services return text usable by any model with function
tools, including local models. They do not require switching the chat provider.
Configure `mods.yml`:

```yaml
deepseek-tools: true
deepseek-tools-api-key-env: DEEPSEEK_API_KEY
# Or supply deepseek-tools-api-key directly.
web-fetch-provider: deepseek # optional; default local needs no API key
```

Set `DEEPSEEK_API_KEY` in your environment. Chat provider keys and the web-search
key are separate settings; no key is copied from them automatically.
`deepseek-tools` enables `document_read` and `audio_transcribe` unless
`builtin-tools.filesystem` is `false`. Neither requires shell tools or a
multimodal chat model. Both use ordinary filesystem read authorization and send
the requested local file to DeepSeek, incurring service charges. They do not
create remote Files API objects or write local files. Results, like other tool
results, become conversation history.

Example prompts (with tools enabled):

```sh
mods 'Read report.pdf with document_read and summarize it with page references.'
mods 'Transcribe note.wav with audio_transcribe and list the action items.'
```

- `document_read`: PDF, Word, PowerPoint and images accepted by DeepSeek OCR;
  mods limits uploads to 32 MiB and responses to 16 MiB. `purpose` defaults to
  `text_recognition`; `rich_layout_recognition` returns raw layout recognition
  text, not image attachments. The whole document is uploaded and recognized,
  even when only one page is requested. Read using 1-based `page` and character
  `offset`/`limit` (default 12,000, maximum 20,000). Follow `next_page` and
  `next_offset` until `done`. `recognition_complete: false` and
  `finish_reason: length` mean that OCR truncated the page; `done` only means
  the available text has been read. Quote the returned source and page number.
  The most recent document's result is cached by content and purpose within
  the current request's tool loop. Another document replaces it; later chat
  turns or resumed sessions may upload and bill again. Cancellation does not
  guarantee cancellation of server-side recognition. Requests are not retried.
- `audio_transcribe`: accepts validated 16-bit PCM WAV, mono or stereo, at most
  6 MiB and 120 seconds. Supported sample rates are 8, 16, 22.05, 24, 32, 44.1
  and 48 kHz. For stereo only the left channel is recognized. This tool does
  not accept MP3/Ogg, record audio, convert formats, or provide streaming ASR.
  The response includes source, transcript, duration and an explicit truncation
  flag (20,000-character output limit). Existing native `--audio` behavior is
  unchanged; this is a separate transcription tool.
- `web-fetch-provider: deepseek`: sends URLs to DeepSeek Fetch and returns
  paginated Markdown using the existing `web_fetch` schema. It is independent
  of `deepseek-tools` and `web-search`. Every call, including pagination, may
  incur a fee. Content is limited to 4 MiB and requests to 30 seconds. Local
  preflight checks reject private initial targets unless
  `MODS_WEB_SEARCH_ALLOW_PRIVATE=1`; remote DNS and redirects are controlled
  by DeepSeek, not mods' local transport. The service can still reject targets.
  There is no automatic fallback between local and remote extraction.

All returned text is untrusted source material. The standalone HTTP client
refuses redirects, bounds responses and timeouts, and never retries potentially
billable requests automatically. OCR and ASR have a 185-second client deadline
(or an earlier caller cancellation).

Sources: [OCR](https://api-docs.deepseek.com/guides/ocr/),
[ASR](https://api-docs.deepseek.com/guides/asr/),
[Fetch](https://api-docs.deepseek.com/guides/search/).
Remote `file_id` document attachments, hosted Flash search, TTS and Ogg ASR are
not implemented by these tools.

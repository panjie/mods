> [!NOTE]
> #### Actively Maintained Fork
>
> The original Mods was [sunset by Charm](https://github.com/charmbracelet/mods) on
> March 9, 2026. **This fork** is actively maintained with new features and fixes.

# Mods

<p>
    <img src="assets/mods-product.png" width="630" alt="Mods product art and type treatment"/>
    <br>
    <a href="https://github.com/panjie/mods/actions"><img src="https://github.com/panjie/mods/workflows/build/badge.svg" alt="Build Status"></a>
</p>

An AI agent for your terminal. Mods reads and edits files, runs shell commands,
searches the web, and chains tool calls until the job is done — with a review
step before anything risky.

**Self-contained by design.** Mods is one Go binary. Its built-in tools run
in-process: no Python, OCR or ASR service, ffmpeg, or nested `mods`/model process
is required to read and edit files, run commands, or work with native image,
audio, and video input. Network access is explicit: the model provider, prompts
loaded from a URL, opt-in web search, `http_download`, and MCP servers you
configure. Portable mode can keep the binary, config, and sessions in a single
folder you can copy anywhere.

**Ask Mods how to use Mods.** Questions about Mods itself are answered from
version-matched self-help generated from the running binary's real flags and
settings, not from stale docs:

```sh
mods "how do I add a custom OpenAI-compatible provider?"
mods "what is the difference between review-mode auto and always?"
mods "where are my sessions stored, and how do I make this install portable?"
```

With a tool-capable provider and tools enabled (the default setup), Mods calls
its `mods_help` tool, so it can check what your version actually supports
instead of inventing options. Prefer local references? Run `mods --help`,
`mods --list-tools`, or `mods --dirs`.

Mods works with [OpenAI], [Anthropic], [Gemini], [Azure OpenAI], [DeepSeek],
[GLM], [Qwen], [Kimi], [MiniMax], [OpenRouter], and local [Ollama] models. Any
OpenAI-compatible endpoint can be added in `mods.yml`.

[OpenAI]: https://platform.openai.com/account/api-keys
[Anthropic]: https://console.anthropic.com/settings/keys
[Gemini]: https://aistudio.google.com/apikey
[Azure OpenAI]: https://azure.microsoft.com/en-us/products/cognitive-services/openai-service
[DeepSeek]: https://platform.deepseek.com/api_keys
[OpenRouter]: https://openrouter.ai/settings/keys
[Ollama]: https://ollama.com
[GLM]: https://open.bigmodel.cn/
[Qwen]: https://dashscope.console.aliyun.com/
[Kimi]: https://platform.moonshot.cn/
[MiniMax]: https://www.minimaxi.com/

<p><img src="assets/demo.gif" width="900" alt="a GIF of mods demonstrating pipelines, web search, image input, and minimal output"></p>

## Quick Start

### Install

```sh
brew install panjie/tap/mods
```

Or, with Go 1.25.8+:

```sh
go install github.com/panjie/mods@latest
```

Release builds are static Go binaries; the built-in toolset needs no extra
runtime.

<details>
<summary>Other ways to install</summary>

Build from source with [Task](https://taskfile.dev):

```sh
git clone https://github.com/panjie/mods.git
cd mods
go run github.com/go-task/task/v3/cmd/task@v3.51.1 build        # binary lands in bin/mods
go run github.com/go-task/task/v3/cmd/task@v3.51.1 install      # installs to ~/.local/bin/mods (or $XDG_BIN_HOME)
```

The `install` task honors `PREFIX`, `BINDIR`, `DESTDIR`, and `XDG_BIN_HOME`.
On all platforms the default install path is `$HOME/.local/bin/mods` (`mods.exe` on Windows).

Prebuilt Windows portable ZIPs are published on the [releases] page.
The latest nightly build is published as the [nightly prerelease].

[releases]: https://github.com/panjie/mods/releases
[nightly prerelease]: https://github.com/panjie/mods/releases/tag/nightly

Generate shell completions:

```sh
mods completion bash > mods.bash
mods completion zsh > _mods
mods completion fish > mods.fish
mods completion powershell > mods.ps1
```

</details>

### Configure

Run the interactive setup wizard to pick a provider, model, and API key:

```sh
mods --config
```

The wizard walks you through provider and model selection, API key entry,
built-in tools, and review mode, then saves everything to `mods.yml`
automatically.

The standard config path is `$XDG_CONFIG_HOME/mods/mods.yml`, or
`$HOME/.config/mods/mods.yml` when `XDG_CONFIG_HOME` is unset. Sessions are
stored in `$XDG_DATA_HOME/mods/sessions`, or `$HOME/.local/share/mods/sessions`
when `XDG_DATA_HOME` is unset. If a `mods.yml` exists next to the executable,
[portable mode](#portable-mode) uses that file instead.

On first real use, if `mods.yml` does not exist and there are no saved
sessions, Mods starts this wizard automatically. Help, version, completion,
directory, settings, config, and list commands do not trigger first-run setup.

Prefer to edit the raw YAML? Open it in `$EDITOR`:

```sh
mods --settings
```

You can also recursively merge a YAML mapping into `mods.yml` from the command
line. Existing values at the same path are overwritten, while unmentioned
settings are preserved:

```sh
mods --settings 'default-api: openai'

mods --settings 'apis:
  fujitsu-google:
    api-type: google
    base-url: https://abc.com
    api-key-env: FUJITSU_GOOGLE_API_KEY
    models:
      gemini-3.0-pro: {}'
```

The `--settings='<yaml>'` spelling is also supported. Avoid passing `api-key`
values directly because command arguments may be recorded in shell history or
visible to other local processes; prefer `api-key-env`.

A minimal provider configuration looks like this:

```yaml
default-api: openai
default-model: gpt-5.4
apis:
  openai:
    api-key-env: OPENAI_API_KEY
    models:
      gpt-5.4: {}
```

```sh
export OPENAI_API_KEY=sk-...
```

See [Provider details](docs/providers.md) for custom endpoints, routing, and
provider-specific reasoning options.

### First Run

```sh
# Summarize piped output
git log --oneline -20 | mods -f "group these commits by theme"

# Let Mods read and edit files in the current directory
mods "read README.md and suggest three improvements"
```

## What Mods Can Do

- **Act, don't just talk.** With tools enabled, Mods inspects and edits files,
  runs shell commands, and chains tool calls to complete a task end-to-end.
- **Simple command review.** Model-declared reads run directly; commands that may
  write ask for approval. Native tools retain their target-based review policy.
- **Fit your pipeline.** `--minimal` prints only the final answer, with no
  formatting — ready for `| gum choose`, `jq`, or another script.
- **Search the live web.** Web search is opt-in. `--web-search` uses a supported
  provider's hosted search when available, otherwise Tavily, DeepSeek, or a custom backend.
- **See and hear media.** Pass images, audio, or video with `--image`, `--audio`,
  or `--video`; Mods reads local files directly into a capable model.
- **Remember everything.** Sessions are saved locally with a title and a short
  ID, then listed, resumed, or deleted from a browser.
- **Extend without rebuilding.** Use [MCP](#mcp) servers, [custom roles](#custom-roles),
  prompt overrides, and [skills](#skills).

## Examples

### Review and Refactor

```sh
# Review uncommitted changes before pushing
git diff | mods -f "review this diff — flag bugs, security issues, and naming problems"

# Migrate a codebase from one library to another
mods "replace all requests usage with httpx, keep behavior identical"

# Sweep the repo for stale patterns
mods "find every console.log and replace it with the structured logger"
```

### Debug and Understand

```sh
# Diagnose why tests are failing
go test ./... 2>&1 | mods "explain each failure and suggest the likely fix"

# Make sense of a messy log tail
journalctl -u myapp --since "1h ago" | mods "what went wrong in the last hour?"

# Get up to speed on an unfamiliar codebase
mods "read internal/tools and explain how a tool call flows end-to-end"
```

### Generate and Automate

```sh
# Draft a changelog from recent commits
git log v1.0.0..HEAD --oneline | mods "write a user-facing changelog grouped by theme"

# Add tests for the most important untested functions
mods "find Go functions without tests and add table-driven tests for the top 5"

# Collect every TODO into a prioritized report
mods "scan for TODO/FIXME comments and write prioritized TODO-report.md"
```

### Pipelines and Sessions

```sh
# Pick from a list using pipeline-friendly output
find . -maxdepth 1 -type f | sort | mods --minimal "pick the five most important files" | gum choose

# Ask the live web, then act on the answer
export TAVILY_API_KEY="tvly-..."
mods --web-search "what changed in the latest Go release? update go.mod if relevant"

# Save, resume, and continue in a chat
mods "draft v1.1 release notes from CHANGELOG.md"
mods --continue-last "turn those into a Twitter thread"
mods --list-sessions
mods --chat
```

### DeepSeek Search

Select DeepSeek in `mods --config`, or add these settings to `mods.yml`:

```yaml
web-search: true
web-search-provider: deepseek
web-search-api-key-env: DEEPSEEK_API_KEY
```

Set `DEEPSEEK_API_KEY` to your DeepSeek API key, or store the key in
`web-search-api-key`. The search backend is independent of the chat model and
uses the [DeepSeek Search API](https://api-docs.deepseek.com/api/search/).
Results include titles, URLs, and model-oriented summaries. Queries support
DeepSeek's search operators and must fit its 128 Unicode character limit.
Provider-hosted search still takes precedence when the selected chat endpoint
supports it (for example, DeepSeek Responses).

To run a live search smoke test with `DEEPSEEK_API_KEY` set:
`go test -tags integration ./internal/websearch -run TestDeepSeekSearchIntegration -count=1 -v`.
This sends one real search request; ordinary tests do not call the service.

## Safety & Review

Command tools (`shell_run`, `powershell_run`, and `process_run`) require the
model to declare `effect` for the entire call:

| Declaration | Behavior in `auto` and `always` |
|-------------|--------------------------------|
| `read_only` | Execute without review. |
| `may_write` | Ask for approval for this call. Use for mutations or uncertainty. |

Mods trusts this declaration; it does not parse commands, prove their effects,
or independently check the model's judgment. A mistaken `read_only` declaration
can therefore execute a mutation without approval. This is a confirmation policy,
not an execution sandbox.

Models are instructed to prefer read-only operations when they correctly satisfy
the task, keep commands clear and reviewable, and declare the final call honestly.
Variables, loops, pipelines and scripts are allowed. Necessary writes should be
performed normally with `may_write`, not avoided or mislabeled to skip approval.
Missing or invalid declarations return an argument error without execution.
Every command also requires a nonblank `purpose` explaining the operation and
its meaningful effects in the user's language. This text is shown for review
and does not affect authorization.

```
Run command?                                    ←/→ choose
Purpose: Delete the obsolete demo image

Command:  /path/to/project
  rm -f demo.gif

 ❯ Y Allow once  ·  N Deny  ·  Ctrl+C Cancel task
```

Long commands can be browsed with Up/Down; approval is available immediately.
Commands do not save or reuse directory/remote permissions and receive no temporary
directory exemption. In `auto` and `always`, `may_write` returns an approval
unavailable error without an interactive review channel. Denial or cancellation
stops the affected operation.

Native filesystem and MCP tools keep their existing policy. Reads run without
review; in `auto`, writes can reuse conversation-scoped directory or remote-origin
rules when those rules cover all targets. Native writes confined to safe temporary
directories can be exempt. Native paths are resolved and rechecked before execution.
`Allow for session` remains available for eligible native/MCP writes. Saved rules
are restored by `--continue` and are not shared across conversations.

Choose `-V` / `--review-mode`, `review-mode` in `mods.yml`, or `MODS_REVIEW_MODE`:

| Mode | Behavior |
|------|----------|
| `auto` | Default. Commands follow their declaration; other tools may reuse saved target rules. |
| `always` | Commands follow the same declaration rule; other tools ignore saved rules. |
| `never` | Disable review. `-N` / `--no-review` is a shortcut. |

The retired `command-simplification` and `builtin-tools.shell-read-only-commands`
settings are ignored when loading old configuration. They no longer appear in new
config files or help. Existing command-related saved rules do not exempt commands;
the database and conversation history remain readable.

On Windows, command tools (`process_run`, `powershell_run`, and `shell_run`)
accept `elevated: true` when one command needs administrator privileges. Mods
applies the declaration-based review policy first, then requests Windows UAC
consent or administrator credentials. Only that command runs elevated; output
and exit status return to the conversation. `--no-review` does not bypass UAC. Neither Windows sudo nor an elevated Mods
session is required.

UAC consent time counts toward the command timeout. Canceling or timing out
disconnects the helper and stops its command tree. Windows may leave a pending
consent dialog visible; accepting it after cancellation cannot start the command.
Enter administrator credentials only into Windows' UAC dialog, never into chat.
An already elevated Mods session uses its existing privileges without another
UAC prompt.

## Tools & MCP

Mods ships its complete toolset in the binary. Filesystem tools auto-activate
when a request needs them; shell tools are enabled by default.

| Built-in | What it does |
|----------|--------------|
| `fs_*` | Read, search, write, replace, patch, move, copy, and delete files. |
| `process_run` | Run one executable with a literal argv and structured output. |
| `shell_run` / `powershell_run` | Run shell syntax: pipelines, redirection, compound commands. |
| `runtime_info` | Report the OS, cwd, selected shell, and command availability. |
| `http_download` | Download up to 32 explicit URLs without a shell. |
| `web_search` | Search the web (requires `--web-search`). |
| `fs_find_images` / `fs_find_media` | Find image, audio, and video candidates recursively. |
| `fs_read_images` / `fs_read_media` | Read original media into the current model. |
| `todo_write` | Keep a visible plan for multi-step work. |
| `request_user_input` | Ask a necessary question with a compact local dialog. |
| `load_skill` / `search_skills` | Load installed [skills](#skills). |
| `mods_help` | Version-matched help for Mods itself. |

Run `mods --list-tools` for the full catalogue, including any MCP tools.

Filesystem paths resolve from the process current working directory. Shell and
process tools start there by default and accept a per-call `cwd` argument. The
status line shows what Mods is doing between tool calls ("read ...",
"shell ...", "search web ..."). Hide it with
`--hide-tool-status`, which also suppresses the compact one-line record each
completed tool call leaves in normal output (for example
`> ✓ shell  ls -la  · exit 0`).

Filesystem tools default to `auto`; shell is enabled by default. Toggle them in
`mods.yml`:

```yaml
builtin-tools:
  filesystem: auto          # auto, true, or false
  shell: true
  shell-timeout: 60s        # default timeout; per-call timeout_ms may override
```

Commands use model-declared effects in both normal and minimal mode. Related reads
and aggregations may share a pipeline or readable script. Existing scripts run
unchanged once authorized; a temporary script location does not exempt a
`may_write` invocation from review.

Git HTTP(S) authentication can prompt in the terminal and continue the same
command when needed; passwords are masked, passed over local IPC, and never sent
to the model or saved by Mods. Debug traces (`-D`/`--debug`) and token counts
(`-s`/`--show-token-usage`) go to stderr. See [Tool details](docs/tools.md) for
command declarations, `http_download`, Git authentication, and debug output.

### MCP

Connect external tools and data sources through
[Model Context Protocol](https://modelcontextprotocol.io) servers configured
under `mcp-servers` in `mods.yml`:

```yaml
mcp-servers:
  github:
    command: docker
    env:
      - GITHUB_PERSONAL_ACCESS_TOKEN=xxxyyy
    args:
      - run
      - "-i"
      - "--rm"
      - "-e"
      - GITHUB_PERSONAL_ACCESS_TOKEN
      - "ghcr.io/github/github-mcp-server"
```

Inspect what's available with `mods --list-mcps` and `mods --list-tools`. MCP is
optional: everything built into Mods works without it.

### Skills

Skills are folders with a `SKILL.md` that teach Mods a workflow. They load on
demand, so only matching instructions enter the context. Mods loads skills
from the configured directories and does not ship built-in skills.

```sh
mods --list-skills
mods --skills-dirs ~/.agents/skills --list-skills
```

Install skills under `~/.agents/skills`, or add directories with `--skills-dirs`.
Later directories override same-name skills. Portable installs also read a
`skills/` directory beside the executable.

## ChatGPT subscription

Run `mods --config`, select **chatgpt**, and choose **Continue with ChatGPT**.
Your system browser opens automatically. Grant plan usage there, then
return to mods to select an account-provided model. The connection check sends a
small inference request using your plan allowance. No API key is required.
If the browser cannot be opened, mods displays the authorization URL as a fallback.

Account management is also available without the wizard:

```sh
mods --chatgpt-auth login                 # add an account/workspace
mods --chatgpt-auth list                  # show registrations; * marks the active one
mods --chatgpt-auth use:<client-id>        # switch the active registration
mods --chatgpt-auth login:<client-id>      # renew a saved registration
mods --chatgpt-auth logout                # sign out of the active registration
mods -a chatgpt -m <configured-model> "Hello"
```

Each workspace registration has its own client ID and credentials. After
switching accounts, rerun `mods --config` to refresh the model choices. Access
tokens refresh automatically, including between tool rounds. Tokens live in
`$XDG_CONFIG_HOME/mods/chatgpt/accounts.json` (or the OS user configuration
directory when unset), separately from `mods.yml` and conversation history.
This storage stays local even when using a portable configuration. Protect this
credential file; it is written with owner-only permissions on Unix. Logout
attempts remote session revocation and clears local tokens while retaining the
registration and host ID. If revocation cannot be confirmed, disconnect mods
in ChatGPT Settings.

This uses OpenAI's official **Sign in with ChatGPT** preview for local open-source
apps and the public Responses endpoint. Account/workspace eligibility and plan
limits apply. Text, images, local tools and MCP function tools use the existing
mods approval flow. This route does not support audio/video input or hosted
Responses tools such as image generation, file search and Code Interpreter.
Unsupported sampling/output-limit parameters are omitted, and complete context
is sent with each request (`store: false`). Usage-limit failures do not fall back
to API-key billing.

Protocol references: [registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in),
[accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions),
[preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).

## Supported Providers

Mods is configured for the providers below out of the box. The easiest way to
get started is `mods --config`; you can also set the matching environment
variable and select a model with `--api` and `--model`.

| Provider     | `--api` value  | Env var                      | Get a key                                     |
|--------------|----------------|------------------------------|-----------------------------------------------|
| OpenAI       | `openai`       | `OPENAI_API_KEY`             | [platform.openai.com][OpenAI]                 |
| ChatGPT      | `chatgpt`      | — (browser sign-in)          | [Subscription setup](#chatgpt-subscription) |
| GitHub Copilot | `github-copilot` | — (device authentication) | `mods --config` |
| Anthropic    | `anthropic`    | `ANTHROPIC_API_KEY`          | [console.anthropic.com][Anthropic]            |
| Google       | `google`       | `GOOGLE_API_KEY`             | [aistudio.google.com][Gemini]                 |
| Azure OpenAI | `azure`        | `AZURE_OPENAI_KEY`           | [azure.microsoft.com][Azure OpenAI]           |
| DeepSeek     | `deepseek`     | `DEEPSEEK_API_KEY`           | [platform.deepseek.com][DeepSeek]             |
| OpenCode Go  | `opencode-go` / `opencode-go-anthropic` | `OPENCODE_GO_API_KEY` | [opencode.ai/auth](https://opencode.ai/auth) |
| OpenRouter   | `openrouter`   | `OPENROUTER_API_KEY`         | [openrouter.ai][OpenRouter]                   |
| Ollama       | `ollama`       | — (local)                    | [ollama.com][Ollama]                          |
| GLM          | `glm`          | `ZAI_API_KEY`                | [open.bigmodel.cn][GLM]                       |
| Qwen         | `qwen`         | `DASHSCOPE_API_KEY`          | [dashscope.console.aliyun.com][Qwen]          |
| Kimi         | `kimi`         | `MOONSHOT_API_KEY`           | [platform.moonshot.cn][Kimi]                  |
| MiniMax      | `minimax`      | `MINIMAX_API_KEY`            | [www.minimaxi.com][MiniMax]                   |

The built-in `openai` provider uses Responses by default when its base URL points
to `api.openai.com`; set `endpoint: chat-completions` on a model to override.
Mods sends `store: false` and keeps the encrypted response items needed for
stateless reasoning and tool continuation in its local session data. Azure
OpenAI uses Chat Completions. Custom provider names and gateway URLs default to
Chat Completions unless an explicit endpoint or the official OpenCode Go routing
table selects Responses. The `chatgpt` subscription provider requires Responses.

With the built-in `deepseek` provider, `deepseek-flash`, `deepseek-v4-flash`,
`deepseek-v4-flash-vision-exp`, and `deepseek-v4-pro` requests to `api.deepseek.com` also use the
[DeepSeek Responses API](https://api-docs.deepseek.com/zh-cn/guides/responses_api)
automatically. DeepSeek Responses is stateless, so complete response items and
tool outputs remain in the local session and are replayed on the next turn.
Mods can send images through both DeepSeek protocols; image acceptance is decided
by the service, without a local model-name allowlist. Set
`endpoint: chat-completions` on the model to opt out of Responses.

Three independent settings control routing:

- `api-type` chooses the transport adapter (`openai`, `anthropic`, `google`,
  `ollama`, `azure`, or `github-copilot`).
- `provider-profile` chooses provider-specific semantics on an OpenAI-compatible
  transport (`openai`, `deepseek`, `qwen`, `glm`, `kimi`, or `minimax`). A
  model-level value overrides the provider default. The `chatgpt` profile is
  reserved for the built-in subscription provider.
- `endpoint` chooses `responses` or `chat-completions` for a model on an
  OpenAI-compatible transport.

Unknown enum values are rejected instead of silently falling back. Custom
providers must configure `base-url`; only built-in providers with a known
official endpoint may omit it. When web search is enabled, an endpoint with
hosted search capability uses it automatically; other endpoints keep the
existing local search behavior.

`-t` / `--think` enables reasoning where supported. Exact effort values remain
model- and provider-dependent, and unsupported configured values fail before the
HTTP request. See [Provider details](docs/providers.md) for custom gateways,
thinking controls, and per-provider behavior.

```sh
mods --api anthropic --model claude-sonnet-4-6 "explain this error"
mods --api glm --model glm-5.2 "把这个日志总结成中文要点"
mods --api ollama --model llama4 "summarize this log"
```

## CLI & Configuration

```sh
mods [OPTIONS] [PROMPT...]

mods --help                 # all options, grouped by purpose
mods --config               # interactive setup wizard
mods --settings             # open mods.yml in $EDITOR
mods --settings '<yaml>'    # recursively merge YAML into mods.yml
mods --dirs                 # show config and session paths

mods --list-sessions        # browse saved sessions
mods --list-tools           # built-in and MCP tools
mods --list-skills          # installed skills
mods --list-roles           # custom roles
mods --list-prompts         # built-in prompts and overrides
mods --list-mcps            # configured MCP servers
```

The setup wizard keeps edits when you go back or switch providers, and saves
only after you confirm the final summary. Use Enter to continue, Esc to go back,
and Ctrl+C to cancel. Outside text inputs and filters, `j`/`k` move down/up and
`q` cancels without saving. In model lists, Space selects, `/` filters, `r`
retries discovery, and `m` switches to manual entry. Use Ctrl+J for a new line in
manual model input and Page Up/Page Down to scroll long content. The heading
shows the current step, and the action row stays in place while discovery runs.
Discovery failures include retry and manual-entry guidance.

Help starts with common examples and groups advanced flags after everyday
options. Descriptions wrap to the terminal width; all public flags remain listed.

Most advanced defaults (model parameters, themes, MCP, debug, format hints) live
in `mods.yml` and are documented inline. Run `mods --reset-settings` to back up
the current file and restore the defaults.

### Sessions & Chat

Sessions are saved locally by default with a title and a short ID; `--no-save`
disables saving the current interaction. `--continue` resumes
a session by ID or title; `--continue-last` resumes the most recent one.
`--list-sessions` opens a full-width, single-column browser. Each session occupies
one row with its short ID, title, model, provider, and update time.
Press `/` to filter sessions, `Enter` to open the full transcript, and `c` to
continue the selected conversation. Space marks sessions; `d` deletes the focused
session and `D` deletes marked sessions after confirmation. Inside a transcript,
`/` searches text, `n`/`N` moves between matches, `w` toggles wrapping, and `Esc`
returns to the list.

During a response, consecutive successful file reads collapse into a summary.
`Ctrl+O` toggles tool arguments and output previews; failures show a short
preview automatically. Live previews are bounded; saved sessions retain the
complete tool results. `Ctrl+P` expands the plan, where Up/Down scrolls through
steps. Press `Ctrl+P` again to return to the compact current-step view. These
shortcuts yield to approval dialogs and input forms.

`--chat` starts a continuous conversation. Press `Enter` for a newline,
`Ctrl+Enter` or `Ctrl+S` to send, and `Ctrl+C` while composing to leave. Each turn is saved to the
same session so you can resume it later with `--continue`.

### Portable Mode

Put a `mods.yml` next to the `mods` executable. Config and sessions then live in
that folder instead of the XDG locations, and a `skills/` directory beside the
executable is loaded as well. The folder can be copied as a self-contained
install. Run `mods --dirs` to see the active paths.

### Custom Roles

Roles are reusable system prompts. Define one in `mods.yml`:

```yaml
roles:
  shell:
    - you are a shell expert
    - you do not explain anything
    - you simply output one liners to solve the problems you're asked
    - you do not provide any explanation whatsoever, ONLY the command
```

Then use it:

```sh
mods --role shell "list the largest files in the current directory"
mods --list-roles
```

### Prompt Overrides

Built-in runtime prompts can also be overridden in `mods.yml`. Run
`mods --list-prompts` to print the available keys and their default text, then
copy the one you want to customize:

```yaml
prompts:
  workflow: |
    Follow the task through implementation and verification.
    Explain the result and any remaining uncertainty.
```

The removed `prompts.shell-classifier` and `shell-classify-prompt` settings are
ignored in existing configuration files and can be deleted.

## Images, Audio, and Video

Mods reads media natively and sends it straight to the model — no OCR, ASR,
ffmpeg, or nested `mods` process:

```sh
mods --image assets/mods-product.png "suggest alt text for this image"
mods -I "describe the image on my clipboard"
mods -a qwen -m qwen3.8-omni-flash --audio meeting.wav "summarize the decisions in this recording"
mods -a qwen -m qwen3.8-omni-flash --video meeting.mp4 "list the action items and who owns each one"
```

`--image`, `--audio`, and `--video` are repeatable and can be combined. Direct
attachments do not need filesystem tools. Automatic discovery and reading
through `fs_find_images`/`fs_read_images` and `fs_find_media`/`fs_read_media`
do; `auto` is the default and recognizes media requests, while
`builtin-tools.filesystem: false` disables them. Describe what you are looking
for and Mods enumerates candidates, reads the originals, and reports matches,
non-matches, and uncertain cases.

- Up to 8 files per media read.
- Images: up to 64 per request and 20 MiB of raw image data.
- Audio: WAV/MP3, 6 MiB and 10 minutes per file, 4 per read, 15 minutes total.
- Video: non-fragmented H.264/AAC-LC MP4, 6 MiB, 2 minutes, and 1080p per file,
  1 per read, 5 minutes total.
- Saved and current input share 20 MiB of raw media and a 40 MiB serialized
  request budget.

Adapters expose media according to their implemented protocol, without a
model-name allowlist or per-model media settings. Audio and video currently
require the `qwen` protocol profile and a model that supports those inputs.
Unsupported adapter input kinds fail locally; the service may reject unsupported
model inputs, and Mods preserves that error instead of silently dropping the
file. Acceptance does not prove recognition. For formats, sampling, pagination,
and uncertainty details, see [Media details](docs/media.md).

## Contributing

Issues and pull requests are welcome on this fork. Before opening a PR, run:

```sh
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check   # compile everything
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test    # run the test suite
```

CI runs the same build and test baseline on Linux, macOS, and Windows. See
[Development](docs/development.md) for linting, focused tests, real-AI black-box
scenarios, and the Windows reliability suite.

## License

[MIT](https://github.com/panjie/mods/raw/main/LICENSE)

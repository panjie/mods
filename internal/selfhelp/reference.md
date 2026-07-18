## Overview

Mods is a terminal AI agent for prompts, pipelines, and local work. It can read
and edit files, run shell commands, search the web, use MCP servers, and chain
multiple tool calls. A built-in review step prompts before mutating changes.

The configuration file is normally `~/.config/mods/mods.yml`, or
`$XDG_CONFIG_HOME/mods/mods.yml` when `XDG_CONFIG_HOME` is set. Portable mode
uses the `mods.yml` next to the executable instead. The `mods_help` result
reports the exact active path for the current process.

Configuration precedence is CLI flags > mods.yml > `MODS_*` environment
variables > defaults. Configuration file changes take effect on the next mods
invocation.

Piping: `data | mods "instruction"` feeds piped stdin as input and the
positional argument as the instruction.

## CLI

Short forms use `-` and one letter; long forms use `--`. `mods --help` shows
every public option grouped by purpose. Less commonly needed options are marked
advanced.

### Model and provider

- `-a`, `--api` — OpenAI-compatible REST API or another configured provider
- `-m`, `--model` — default model or configured model alias
- `-M`, `--ask-model` — interactively choose a model
- `--max-tokens <num>` — maximum response tokens
- `--no-limit` — disable the client-side input-size limit
- `--max-retries <num>` — maximum API retries
- `-x`, `--http-proxy` — HTTP proxy for API requests

### Modes and sessions

- `--chat` — start a continuous session
- `-p`, `--plan` — create a plan for review before executing changes
- `-t`, `--think` — enable extended thinking
- `-C`, `--continue <title>` — continue a saved session by title
- `-c`, `--continue-last` — continue from the last response
- `-l`, `--list-sessions` — browse, search, copy IDs, and delete sessions
- `-n`, `--no-save` — disable saving and resuming sessions

### Prompt and context

- `-e`, `--editor` — edit the prompt in `$EDITOR`
- `-R`, `--role <name>` — use a role defined under `roles`
- `--list-roles` — list configured roles
- `-i`, `--image <path>` — attach png, jpg, gif, or webp images
- `--stdin-image` — treat piped stdin as raw image data
- `-I`, `--clipboard-image` — attach the current clipboard image
- `--no-instructions` — do not load workspace-root `AGENTS.md`
- `--list-prompts` — list built-in prompts and prompt templates

### Workspace and review

- `--workspace <dir>` — set the filesystem and shell workspace
- `-V`, `--review-mode <mode>` — use `auto`, `always`, or `never`

### Tools and integrations

- `--max-tool-rounds <num>` — maximum tool-call rounds; zero uses 30
- `--list-tools` — list native and MCP tools
- `--skills-dirs <dir>` — add a skill directory; may be repeated
- `--list-skills` — list installed skills
- `--web-search` — enable or disable web search
- `--list-mcps` — list configured MCP servers

### Output and display

- `-f`, `--format [markdown|json|<custom>]` — request an output format
- `--minimal` — emit only the final pipeline-friendly result
- `--raw` — render raw text on a TTY
- `--word-wrap <width>` — set output wrapping width
- `--hide-tool-status` — hide the tool-operation label
- `--show-token-usage` — print token usage after each interaction

### Configuration and maintenance

- `--config` — open the interactive setup wizard
- `--settings` — edit the active config in `$EDITOR`
- `--dirs` — print active config and session directories
- `--reset-settings` — back up and reset the config

### Help and diagnostics

- `-h`, `--help` — show CLI help
- `-v`, `--version` — show the version
- `-D`, `--debug` — print request, tool, and execution diagnostics

## Config

The active path is reported by `mods_help`. On Windows, the normal default is
`%USERPROFILE%\.config\mods\mods.yml`; use PowerShell syntax such as
`Get-Content (Join-Path $env:USERPROFILE ".config\mods\mods.yml")`.

For a non-secret change, inspect only the relevant range with `fs_search` and
`fs_read_file`, then use `fs_replace` with exact current text. Preserve comments
and unrelated values. Reads and writes outside the workspace use mods' normal
directory approval flow. Do not read or echo API keys. Prefer `api-key-env`
instead of putting a secret directly in YAML.

Top-level persistent keys:

- `default-api` — active provider name
- `default-model` — model name or alias configured under the active API
- `format` — `markdown`, `json`, custom format name, or empty
- `format-text` — custom format prompts by name
- `minimal` — pipeline-friendly output by default
- `raw` — raw terminal rendering
- `hide-tool-status` — hide tool-operation labels
- `show-token-usage` — show input/output token usage
- `word-wrap` — terminal wrapping width
- `max-tokens` — maximum response tokens
- `max-input-chars` — client-side input character limit
- `no-limit` — disable that input limit
- `no-instructions` — disable automatic `AGENTS.md`
- `http-proxy` — API HTTP proxy
- `max-retries` — API retry limit
- `max-tool-rounds` — tool-call round limit
- `theme` — `charm`, `catppuccin`, `dracula`, or `base16`
- `review-mode` — `auto`, `always`, or `never`
- `think` — enable extended thinking by default
- `role` — active role
- `roles` — role name to a list of inline or `file://` system messages
- `prompts` — overrides for `identity`, `tool-selection`, `plan`, and
  `shell-classifier`; empty values retain defaults
- `builtin-tools` — native filesystem and shell settings
- `web-search` — enable web search
- `web-search-provider` — `duckduckgo`, `tavily`, or `custom`
- `web-search-api-key` — search API key; prefer the environment form
- `web-search-api-key-env` — environment variable containing the search key
- `mcp-servers` — configured stdio, SSE, or HTTP MCP servers
- `mcp-timeout` — MCP connection/operation timeout
- `apis` — provider definitions
- `images` — default image paths
- `stdin-image` — treat stdin as image bytes
- `clipboard-image` — attach the clipboard image
- `shell-classify-prompt` — legacy classifier override; prefer
  `prompts.shell-classifier`
- `skills-dirs` — installed skill directories
- `debug` — legacy persisted debug setting

Example role:

```yaml
roles:
  shell:
    - you are a shell expert
    - file:///home/user/shell-instructions.txt
```

Example prompt overrides:

```yaml
prompts:
  identity: ""
  tool-selection: ""
  plan: ""
  shell-classifier: ""
```

Example native tools:

```yaml
builtin-tools:
  filesystem: auto
  shell: true
  shell-read-only-commands: []
  shell-timeout: 30s
  shell-max-output: 20000
  workspace: ""
```

`builtin-tools.filesystem` accepts `auto`, `true`, or `false`. Auto enables file
tools when the current or recent request concerns files or a mods config
mutation. `builtin-tools.shell-read-only-commands` trusts every argument and
subcommand for each listed executable, while unsafe shell structures such as
redirection are still reviewed.

## Providers

Providers are configured only under `apis`:

```yaml
apis:
  <name>:
    base-url: https://api.example.com/v1
    api-key: ""
    api-key-env: EXAMPLE_API_KEY
    api-key-cmd: ""
    api-type: openai
    models:
      <model-name>:
        aliases: ["short"]
        max-input-chars: 100000
        fallback: ""
        thinking-type: ""
        thinking-budget: 0
        reasoning-effort: medium
        thought-fields: []
        think-tag: think
        extra-params: {}
```

Supported `api-type` values include `openai`, `anthropic`, `ollama`, `google`,
`azure`, and `azure-ad`. Custom OpenAI-compatible endpoints normally use
`api-type: openai` and their own `base-url`.

The default config has no provider model lists. `mods --config` discovers models
when possible and saves only selected models; otherwise enter model identifiers
manually.

`-t` or `think: true` enables thinking where supported. Built-in adapters choose
provider-appropriate defaults. `thinking-type`, `thinking-budget`, and
`reasoning-effort` are optional overrides.

Credential lookup uses `api-key`, `api-key-env`, or `api-key-cmd`. Prefer an
environment variable so the config and model context do not contain the secret.

## Tools

Prefer native `fs_*` tools for direct file operations because paths and approval
intent are structured. Read-only tools include `fs_read_file`, `fs_list_dir`,
`fs_stat`, `fs_search`, and `fs_largest`. Mutation tools include
`fs_write_file`, `fs_replace`, `fs_apply_patch`, `fs_delete_file`,
`fs_delete_dir`, `fs_move`, `fs_copy`, and `fs_mkdir`.

Use `fs_replace` for a small exact edit after reading the target and
`fs_apply_patch` for multi-file or git-style changes. Use `fs_delete_file` only
for files and `fs_delete_dir` only for directories.

Shell tools are appropriate for repository-wide inspection, builds, tests, git,
package managers, and pipelines. On Windows, native shell tools execute
PowerShell; commands should be compatible with Windows PowerShell 5.1.

`review-mode: auto` allows recognized reads and reviews mutations.
`review-mode: always` reviews tool access even when read-only.
`review-mode: never` suppresses interactive review. Directory approvals are
conversation-scoped and distinguish read from write.

Web search providers are `duckduckgo`, `tavily`, and `custom`. Custom search
targets reject private and loopback addresses unless
`MODS_WEB_SEARCH_ALLOW_PRIVATE=1` is explicitly set.

Each `mcp-servers` entry supports `type`, `command`, `args`, `env`, `url`, and
`pass-env-all`. MCP tools declaring `readOnlyHint: true` skip mutation review in
auto mode; unannotated tools are treated as mutable.

## Skills

When a request matches an installed skill description, call
`load_skill(<name>)`, then follow the returned instructions. Load auxiliary
files with `load_skill(<name>, "<relative-file>")` only when the skill directs
you to them.

Skills normally live in `~/.agents/skills/<name>/SKILL.md`. Portable mode also
loads `<mods executable directory>/skills`. Add directories with repeated
`--skills-dirs`, the `skills-dirs` config key, or `MODS_SKILLS_DIRS` using the
OS path-list separator. Later directories override earlier same-name skills.

Mods loads only installed skills; it does not search for or install skills.
Loaded skill content remains in the conversation and should not be loaded twice.

## Portable

Portable mode is active when a `mods.yml` exists next to the executable. Config
and sessions then live beside the executable, and `XDG_CONFIG_HOME` and
`XDG_DATA_HOME` are ignored. The folder can be copied as a self-contained unit.

To bootstrap a new portable folder, run `mods --config`, choose `Portable` for
the config location, and restart mods. `mods --dirs` prints the active config
and session directories.

User skill directories are still included in portable mode, with the executable
skill directory added as another source.

## Troubleshooting

- Missing API key: inspect `apis.<name>.api-key-env`, then `api-key` and
  `api-key-cmd`; never print the key.
- Model not found: ensure `default-model` exists under the selected
  `apis.<name>.models`, or rerun `mods --config`.
- Tool unavailable: inspect `builtin-tools.filesystem`,
  `builtin-tools.shell`, provider tool support, and the provider `api-type`.
- Responses not formatted: inspect `format` and `format-text`.
- Slow requests: inspect `http-proxy`, model choice, `max-tokens`, tool count,
  and `max-input-chars`.
- Config changes not visible: start a new mods invocation; the running process
  does not hot-reload `mods.yml`.
- Wrong config path: use `mods_help` or `mods --dirs`; portable mode and
  `XDG_CONFIG_HOME` change the location.
- For full terminal reference, run `mods --help`; for guided setup, run
  `mods --config`; to edit manually, run `mods --settings`.

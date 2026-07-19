## Overview

Mods is a terminal AI agent for prompts, pipelines, and local work. It can read
and edit files, run shell commands, search the web, use MCP servers, and chain
multiple tool calls. A built-in review step prompts before mutating changes.

The active config path depends on the platform and portable mode. The
`mods_help` result reports the exact path and current filesystem mode.
Configuration precedence is CLI flags > mods.yml > `MODS_*` environment
variables > defaults. Config changes take effect on the next mods invocation.

Piping `data | mods "instruction"` feeds piped stdin as input and the
positional argument as the instruction.

## CLI

Short forms use `-` and one letter; long forms use `--`. The options below are
generated from the same registered flags, categories, descriptions, and
visibility metadata used by `mods --help`. Runtime flag defaults are omitted
because they may contain values loaded from the user's config.

## Config

For a non-secret change, inspect only the relevant range with `fs_search` and
`fs_read_file`, then use `fs_replace` with exact current text. Preserve comments
and unrelated values. Reads and writes outside the workspace use mods' normal
directory approval flow. Do not read or echo API keys. Prefer `api-key-env`
instead of putting a secret directly in YAML.

The settings below are generated from the persisted config types and the safe
defaults returned by mods itself. Dynamic mapping names use placeholders such
as `<provider>`, `<model>`, and `<server>`. Secret-bearing and opaque fields
never include their value.

Example role:

```yaml
roles:
  shell:
    - you are a shell expert
    - file:///home/user/shell-instructions.txt
```

`builtin-tools.filesystem` accepts `auto`, `true`, or `false`. Auto enables file
tools when the current or recent request concerns files or a mods config
mutation. Trusted shell command names apply to every argument and subcommand;
unsafe shell structures such as redirection are still reviewed.

## Providers

Providers are configured under `apis.<provider>`. Custom OpenAI-compatible
endpoints normally use `api-type: openai` and their own `base-url`. The default
config has no provider model lists; `mods --config` discovers models when
possible and otherwise accepts model identifiers entered manually.

`-t` or `think: true` enables thinking where supported. Provider-specific model
settings can override thinking type, budget, reasoning effort, returned thought
fields, and inline thought tags.

Credential lookup uses `api-key`, `api-key-env`, or `api-key-cmd`. Prefer an
environment variable so neither the config nor model context contains the
secret. The protocol and built-in provider facts below come from the same
metadata used by runtime provider routing and the setup wizard.

## Tools

Prefer native `fs_*` tools for direct file operations because paths and
approval intent are structured. Use exact replacement for a small edit after
reading the target, and patching for coordinated or multi-file edits.

Shell tools are appropriate for repository-wide inspection, builds, tests,
git, package managers, and pipelines. On Windows, native shell tools execute
PowerShell and commands should remain compatible with Windows PowerShell 5.1.

`review-mode: auto` allows recognized reads and reviews mutations;
`review-mode: always` reviews all external access; `review-mode: never`
suppresses interactive review. Directory approvals are conversation-scoped and
distinguish read from write.

MCP tools declaring `readOnlyHint: true` can use the read-only path in auto
mode; unannotated tools are treated as mutable. User MCP tools are intentionally
not enumerated here. The complete built-in catalog below is generated from the
same registered ToolSpecs and capabilities used by `mods --list-tools`.

## Skills

When a request matches an installed skill description, call
`load_skill(<name>)`, then follow the returned instructions. Load auxiliary
files only when the skill directs you to them.

Skills normally live in the user agent skills directory. Portable mode also
loads the executable's `skills` directory. Additional directories can be
provided through the repeated skills-directory option, its config key, or its
`MODS_*` environment form using the OS path-list separator. Later directories
override earlier same-name skills.

Mods loads only installed skills; it does not search for or install skills.
Loaded skill content remains in the conversation and should not be loaded
twice. Installed skill names are intentionally not included in self-help.

## Portable

Portable mode is active when a `mods.yml` exists next to the executable. Config
and sessions then live beside the executable, and the normal XDG config and
data locations are ignored. The folder can be copied as a self-contained unit.

To bootstrap a portable folder, run the interactive config wizard, choose the
portable config location, and restart mods. The directory-reporting command
prints the active config and session directories. User skill directories remain
available, with the executable skill directory added as another source.

## Troubleshooting

- Missing API key: inspect the configured provider's key environment variable,
  then its direct key and key command; never print the resolved key.
- Model not found: ensure the default model exists under the selected provider,
  or rerun the config wizard.
- Tool unavailable: inspect filesystem and shell settings, provider tool
  support, and the provider protocol.
- Responses not formatted: inspect the format selection and custom format
  prompts.
- Slow requests: inspect the proxy, model, response token limit, tool count,
  and input character limit.
- Config changes not visible: start a new mods invocation; the running process
  does not hot-reload `mods.yml`.
- Wrong config path: use `mods_help` or the directory-reporting command;
  portable mode and XDG environment settings change the location.
- For terminal reference, run `mods --help`; for guided setup, run the config
  wizard; to edit manually, use the settings editor command.

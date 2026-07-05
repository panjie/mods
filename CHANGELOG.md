# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [3.2.0] - 2026-07-05

### Removed
- Dropped the unused `--temp`, `--topp`, `--topk`, and `--stop` flags, their config keys/env vars, and the matching provider request fields. mods no longer sends any sampling temperature, top-p, top-k, or stop sequences on model requests, so providers now use their own defaults. (`proto.Request.Temperature` is retained internally for the shell classifier, which pins it to `0` for deterministic classification.)

### Fixed
- Windows `shell_run` now classifies under the same Windows PowerShell 5.1 grammar that executes it, closing a classifier/executor divergence where PS7-only operators (`&&`, `||`, `??`) could bypass the read-only fast-path under a `pwsh.exe` classifier but then fail under the `powershell.exe` executor.
- Legacy session-schema migration now runs in a single transaction, and default-storage relocation moves WAL/-shm companions before the main DB to avoid orphaning uncheckpointed approval rules.

## [3.0.0] - 2026-07-03

### Added
- Directory-centric approval matrix shared by filesystem and shell tools, including read/write-scoped `DirAllow` rules.
- AST-based POSIX shell and PowerShell read-only classifiers, with PowerShell path extraction for review summaries and approvals.
- Google/Gemini function-calling support and Anthropic-compatible provider routing via `api-type: anthropic`.
- Configurable runtime prompt catalog with `prompts` settings and `mods --list-prompts`.
- Shell command result blocks, review operation summaries, and GoReleaser-based tagged release automation with Homebrew tap updates.

### Changed
- Windows tool guidance now prefers `powershell_run` and PS 5.1-compatible syntax.
- `mods --config` can discover models from provider APIs and handles Anthropic-compatible base URLs more consistently.
- `-c` now maps to `--continue-last`; `-C` now maps to `--continue <title>`.
- Built-in identity/help guidance was expanded so models can answer more mods usage and configuration questions.
- Approval and path authorization now pass approved external directories through context before tool execution.

### Fixed
- Hardened MCP subprocess environment filtering, remote MCP URL validation, patch path validation, and safe-workspace checks.
- Fixed Gemini streaming/history handling, Cohere nil stream/tail message panics, and config error fallback behavior.
- Fixed model discovery edge cases, configured API key error labels, provider routing overrides, and shell external path expansion.
- Fixed XDG install-path detection to honor only `XDG_BIN_HOME` or `XDG=1`.
- Stabilized release-prep tests against environment leakage and platform-specific temp directory behavior.

## [2.5.0] - 2026-06-27

### Added
- Interactive setup wizard (`mods --config`) and first-run configuration.
- Continuous chat mode for longer interactive sessions.
- Mods identity system prompt so models understand the CLI and tools.
- Safe workspace filesystem tools (`fs_read` / `fs_write` / `fs_list` / `fs_stat`).
- Plan mode improvements: multi-proposal selection, local shell classification.
- Configurable `web-search-api-key-env` (default `TAVILY_API_KEY`).
- Clipboard image shorthand flag and nightly release automation.

### Changed
- Config precedence: CLI flags > mods.yml > MODS_* env > defaults.
- Custom OpenAI endpoints now configured via wizard / mods.yml, not env auto-discovery.
- Tool approval moved from command prefix matching to directory-scoped grants (`DirAllow`).
- Build system from Mage to Taskfile; reasoning controls now actively enable/disable per provider.

### Fixed
- `-T` reasoning respected in plan mode; loading animation suppressed in debug mode.
- Cached output / config path flushing, CLI input validation before provider calls.
- Config wizard navigation, base URL handling, and Shift+Tab support.

## [2.0.0] - 2026-06-13

### Added
- Plan mode (`--plan` / `-p`) for task planning before execution.
- Reasoning configuration per provider, including DeepSeek and Chinese provider templates.
- `-T` short flag for reasoning-on-by-default, model-level extra params for API customization.
- Arrow key navigation for approval prompts.

### Changed
- Module renamed from `github.com/charmbracelet/mods` to `github.com/panjie/mods`.
- Removed `--prompt` / `--prompt-args` flags; updated to latest provider models.
- CLI help surface reduced and README rewritten to showcase agent capabilities.

### Fixed
- Security gaps in patch, Google auth, and web search providers.
- Cache atomic writes, flag parsing errors, and tool support edge cases.

## [1.0.1] - 2026-06-10

### Added
- Configurable workspace root, shell detection for non-POSIX shells, and conversation-scoped approval rules.
- Direct PowerShell tool on Windows; config entries for new settings.

### Changed
- Build system from Makefile to Mage with XDG-compliant install targets.
- CLI internals moved under `internal/` packages.
- Newline key binding changed to Ctrl+J in the prompt editor.
- Shell classifier switched to LLM-only for reliability.

### Fixed
- Nil pointer panic in Google stream on API failure; mutex copy, config permissions, nil DB guard.
- Windows: cmd.exe output decoded from system ACP to UTF-8; non-zero exit codes treated as successful tool calls.
- Glamour init error handling; harden shell approval chain and concurrency safety.

## [1.0.0] - 2026-06-09

### Added
- Unified `--reasoning` option with auto mode and thinking debug output.
- Tool execution review with configurable shell safety heuristics.
- LLM-based shell command classification for mutable review.
- Pipeline mode for non-interactive usage; status line between tool calls.

### Changed
- Shell safety review uses LLM classification instead of static heuristics.
- DuckDuckGo as default web search engine; config split into client factory.
- Help flags sorted alphabetically.

### Fixed
- Azure OpenAI (non-AD) support; concurrent access and error handling bugs.
- Review deadlock in pipe mode and Ctrl+C deadlock on review channel.
- Cross-platform compatibility; glamour newline collapsing; empty LLM response due to low MaxTokens.

[3.2.0]: https://github.com/panjie/mods/compare/v3.0.0...v3.2.0
[3.0.0]: https://github.com/panjie/mods/compare/v2.5.0...v3.0.0
[2.5.0]: https://github.com/panjie/mods/compare/v2.0.0...v2.5.0
[2.0.0]: https://github.com/panjie/mods/compare/v1.0.1...v2.0.0
[1.0.1]: https://github.com/panjie/mods/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/panjie/mods/releases/tag/v1.0.0

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [2.5.0] - 2026-06-27

### Added
- Interactive setup with `mods --config`, plus first-run configuration when no settings or conversations exist.
- Provider and model setup wizard entries, including web search configuration and clearer base URL handling.
- Continuous chat mode for longer interactive sessions.
- Built-in Mods identity guidance so models understand the CLI, config, tools, and expected behavior.
- Safe workspace filesystem tools (`fs_read`, `fs_write`, `fs_list`, `fs_stat`) with scoped access.
- Plan mode improvements with multi-proposal selection and local shell command classification.
- Configurable `web-search-api-key-env`, defaulting to `TAVILY_API_KEY`.
- Clipboard image shorthand flag.
- Nightly release automation using GoReleaser.

### Changed
- Config precedence is now explicitly `CLI flags > mods.yml > MODS_ env > defaults`.
- Custom OpenAI-compatible endpoints now favor explicit config via `mods --config` or `mods.yml` instead of base URL environment auto-discovery.
- Tool approval rules now use directory-scoped grants instead of shell command prefix matching.
- Reasoning controls now actively enable or disable provider-specific thinking behavior, including DeepSeek.
- The build system moved from Mage to Taskfile.
- Streaming, request handling, and tool policy internals were centralized and optimized.
- Thinking and review UI output was softened and simplified.

### Fixed
- Respect `-T` reasoning settings in plan mode and provider-specific reasoning paths.
- Suppress loading animation in debug mode and preserve unsolicited reasoning output.
- Separate model responses cleanly after tool calls.
- Flush cached output and config path updates reliably.
- Validate CLI inputs before provider calls.
- Improve config wizard navigation, provider model setup, base URL screens, and Shift+Tab behavior.
- Fix always-allow directory rule handling and trust LLM shell review analysis.
- Clarify LLM prompt guidance and update README test expectations.

## [1.0.0] - 2026-06-09

### Added
- Unified `--reasoning` option with auto mode and thinking debug output
- Tool execution review mechanism with configurable shell safety heuristics
- LLM-based shell command classification for mutable review mode
- Pipeline mode for non-interactive/programmatic usage
- Status line showing model activity between tool calls
- Release target in Makefile with version injection via `-ldflags`

### Changed
- Shell command safety review now uses LLM classification instead of static heuristics
- Help flags are now sorted alphabetically
- DuckDuckGo used as the default web search engine
- Config split into separate concerns with client factory extraction
- Tool reviewer extracted into its own module

### Fixed
- Debug flag not syncing to atomic bool; improved shell classifier reliability
- Complete Azure OpenAI (non-AD) support end-to-end
- Concurrent access bugs, error handling gaps, and code duplication
- Review deadlock in non-interactive (pipe) mode
- Ctrl+C deadlock when reviewChan is not closed
- Cross-platform compatibility issues
- Glamour output single newlines collapsed into spaces (preserved via markdown hard line breaks)
- Empty LLM response in shell classification due to `MaxTokens=10` limit

[2.5.0]: https://github.com/panjie/mods/compare/v2.0.0...v2.5.0
[1.0.0]: https://github.com/panjie/mods/releases/tag/v1.0.0

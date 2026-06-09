# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[1.0.0]: https://github.com/panjie/mods/releases/tag/v1.0.0

# AGENTS.md

## Commands
- Install Mage if missing with `go install github.com/magefile/mage@latest`; `mage build` writes `bin/mods` or `bin/mods.exe` with git version metadata.
- Pre-PR baseline from README/CI: `mage check` (`go build ./...`) then `mage test` (`go test ./...`). To mirror CI exactly, run `go build -v ./...` then `go test -v -cover -timeout=30s ./...`.
- Focus a normal test with `go test ./internal/app -run TestName -count=1` or the relevant package path.
- Root Mage task tests are behind `//go:build mage`; run `go test -tags mage . -run TestInstallDir -count=1` after touching `magefile.go` or install-path logic.
- Provider integration tests are excluded by default; run `go test -tags integration ./internal/app -run TestOpenAIIntegration -count=1` only with the matching provider key. Ollama integration uses `OLLAMA_HOST` or `http://localhost:11434` and model `llama4:16x17b`.
- The golden test in `internal/proto` updates with `go test ./internal/proto -run TestStringer -update`.
- Lint config is `.golangci.yml` v2; local lint is `golangci-lint run ./...` if installed. It enables `gofumpt`/`goimports` formatters and sets `run.tests: false`.

## Architecture
- Entry path is `main.go` -> `internal/cli.Run` -> Cobra root in `internal/cli/main.go`; `execute` loads settings with `config.Ensure`, initializes flags after config, and opens the conversation DB unless doing completion/help/version.
- Runtime is `internal/app.Mods` (Bubble Tea): stdin/cache/plan mode, provider selection, streaming, tool calls, and review prompts.
- Provider-neutral contracts are `internal/proto` and `internal/stream`; provider adapters live in `internal/openai`, `internal/anthropic`, `internal/google`, `internal/cohere`, and `internal/ollama`. OpenAI-compatible providers route through `internal/openai`.
- Tool wiring is `internal/tooling.BuildRegistry`: native tools in `internal/tools`, MCP in `internal/mcpclient`, review rules in `internal/approval`. Tools are supported for OpenAI-compatible, Anthropic, and Ollama; Cohere/Google skip implicit auto filesystem tools and error when tools are explicitly enabled.
- Config defaults are generated from embedded `internal/config/config_template.yml`; when adding settings, update the config structs, `Help` map, template, and config tests together.
- Conversation history and saved approval rules persist in a SQLite DB under the configured cache path; schema changes belong in `internal/conversation/db.go` migrations and legacy tests.

## Quirks
- Config precedence is intentionally `CLI flags > mods.yml > MODS_ env > defaults`; `config.Ensure` parses env before YAML so the file can override env.
- Custom OpenAI-compatible endpoints are configured as providers under `apis.<name>.base-url`; use `mods --config` or edit `mods.yml` instead of environment-variable auto-discovery.
- Custom web-search providers reject private/loopback targets unless `MODS_WEB_SEARCH_ALLOW_PRIVATE=1` is set; keep that SSRF guard covered by `internal/websearch` tests.
- `mage install` path precedence is `BINDIR` > `PREFIX/bin` > XDG local bin > default (`/usr/local/bin`, or `%USERPROFILE%\.local\bin` on Windows); `DESTDIR` wraps the final path.
- CI runs on Ubuntu, macOS, and Windows; platform-specific code is split with build tags in `internal/tools`, `internal/platform`, and `internal/clipboard`.

# AGENTS.md

## Commands
- `go run github.com/go-task/task/v3/cmd/task@v3.51.1 build` writes `bin/mods` or `bin/mods.exe` with git version metadata.
- Pre-PR baseline from README/CI: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` (`go build ./...`) then `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test` (root module plus `internal/huh`). To mirror CI exactly, run `go run github.com/go-task/task/v3/cmd/task@v3.51.1 ci` (verbose build/tests with coverage for both modules).
- Focus a normal test with `go test ./internal/app -run TestName -count=1` or the relevant package path.
- Build task install-path tests live in `internal/buildtask`; run `go test ./internal/buildtask -run TestInstallDir -count=1` after touching `Taskfile.yml` or install-path logic.
- Provider integration tests are excluded by default; run `go test -tags integration ./internal/app -run TestOpenAIIntegration -count=1` only with the matching provider key. Ollama integration uses `OLLAMA_HOST` or `http://localhost:11434` and model `llama3.1`.
- The golden test in `internal/proto` updates with `go test ./internal/proto -run TestStringer -update`.
- Lint config is `.golangci.yml` v2; local lint is `golangci-lint run ./...` if installed. It enables `govet` and `ineffassign` linters, the `gofmt` and `goimports` formatters, and sets `run.tests: false`.

## Architecture
- Entry path is `main.go` -> `internal/cli.Run` -> Cobra root in `internal/cli/main.go`; `execute` loads settings with `config.Ensure`, initializes flags after config, and opens the conversation DB unless doing completion/help/version.
- Runtime is `internal/app.Mods` (Bubble Tea): stdin/cache/plan mode, provider selection, streaming, tool calls, and review prompts.
- Provider-neutral contracts are `internal/proto` and `internal/stream`; provider adapters live in `internal/openai`, `internal/anthropic`, `internal/google`, and `internal/ollama`. OpenAI-compatible providers route through `internal/openai`.
- Tool wiring is `internal/tooling.BuildRegistry`: native tools in `internal/tools`, MCP in `internal/mcpclient`, review rules in `internal/approval`. Tools are supported for OpenAI-compatible, Anthropic, and Ollama; Google skips implicit auto filesystem tools and errors when tools are explicitly enabled.
- Config defaults are generated from embedded `internal/config/config_template.yml`; when adding settings, update the config structs, `Help` map, template, and config tests together.
- Conversation history and saved approval rules persist in a SQLite DB under the configured cache path; schema changes belong in `internal/conversation/db.go` migrations and legacy tests.
- Approval/review subsystem (security-critical): `internal/app/review.go` `toolReviewer.requestApproval` gates every tool call. Rule types in `internal/approval/rules.go`: `DirAllow` (directory-scoped, carries a read/write `Mode`), `EditAll`, `ToolAll`, `ShellPrefix`, `ShellExact`. Matching in `matching.go` (`Allows`/`RulesAllowDirs`/`RulesForDirs`); access matrix in `access.go` (`ClassifyAccess` + `AccessIntent`). `DirAllow` rules with empty `Mode` are legacy and match both read+write.
- Shell classification: `internal/app/shell_classify.go` `analyzeShellCommand` — local read-only heuristic fast-path, then LLM classifier (LRU-cached), then `extractExternalPaths` regex merge → `shellCommandAnalysis{NeedsReview, AffectedDirs, Reason}`; `!NeedsReview` ⇒ read. `AffectedDirs` are normalized to directories in `requestApproval`.
- Path authorization: `buildAccessIntent` (review.go) → approved external dirs injected via `WithAuthorizedDirs`/`AuthorizedDirs` context → `internal/tools/path_safety.go` `resolveWorkspacePath` honors them. Approval (pre-Call) and path enforcement (in-Call) decouple only via context.
- `internal/app` map: `mods.go` (model), `request_session.go` (tool-call orchestration), `review.go`+`review_summary.go` (approval UI/labels), `shell_classify.go`, `provider.go`/`stream.go`/`stream_runner.go`, `plan.go` (plan mode), `cache_ops.go`, `prompts.go`, `tool_support.go`, `aliases.go` (re-exports approval/config/ui types into the app package).
- DB migrations: `internal/conversation/db.go` uses a replace-table pattern (`migrateApprovalRulesReplaceTable`) for primary-key changes (SQLite can't ALTER PK in place); guard each new column with `hasColumn` + a migration before the table is opened.
- Design docs for major subsystems live in `docs/superpowers/specs/` and `docs/superpowers/plans/`; consult them for context on non-trivial subsystems.

- Self-help identity prompt: `internal/prompts/identity.md` carries only behavioral policy (not a catalog of flags or config keys). CLI flags are exposed to the model at runtime by introspecting the pflag set in `internal/cli/self_help.go` `selfHelpFlagGroups` (Name/Shorthand/Description/Advanced), which feeds the `mods_help` tool via `registeredSelfHelpFlags`; no entry in identity.md is needed when adding a flag, but every public flag should be listed in `flagCategorySpecs` (`internal/cli/flags.go`) so it lands in the right group (uncategorized flags fall through to an `Other` bucket). `TestSelfHelpCatalogMatchesEveryPublicFlag` in `internal/cli/main_test.go` enforces that every non-hidden flag appears in the runtime catalog; `TestIdentityHasSelfHelpPolicy` in `internal/prompts/prompts_test.go` guards identity.md's policy text.

## Quirks
- Config precedence is intentionally `CLI flags > mods.yml > MODS_ env > defaults`; `config.Ensure` parses env before YAML so the file can override env.
- Custom OpenAI-compatible endpoints are configured as providers under `apis.<name>.base-url`; use `mods --config` or edit `mods.yml` instead of environment-variable auto-discovery.
- Custom web-search providers reject private/loopback targets unless `MODS_WEB_SEARCH_ALLOW_PRIVATE=1` is set; keep that SSRF guard covered by `internal/websearch` tests.
- The Task `install` path precedence is `BINDIR` > `PREFIX/bin` > XDG local bin > default (`/usr/local/bin`, or `%USERPROFILE%\.local\bin` on Windows); `DESTDIR` wraps the final path.
- CI runs on Ubuntu, macOS, and Windows; platform-specific code is split with build tags in `internal/tools`, `internal/platform`, and `internal/clipboard`.
- `DirAllow` approval rules are conversation-scoped (persisted in `approval_rules`, restored via `--continue`, not shared across conversations); a read approval does not grant write and vice versa (mode-split). Legacy rules with empty `Mode` match both.

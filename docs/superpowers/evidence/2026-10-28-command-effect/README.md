# Command-effect declaration verification

Date: 2026-10-28. Host: Windows amd64, Go 1.26.3, CGO disabled.
Contract: [model-declared command effects](../../specs/2026-10-28-command-effect-declaration.md).

## Runtime and regression checks

- Task v3.51.1 `check`, `test`, and `build` passed. The build produced
  `bin/mods.exe`; `--version` reported `v4.5.0-39-g2c7665b-dirty (2c7665b)`.
- App, tools, approval, config and prompts package tests also passed uncached.
- Command tests cover all three command tools, both declarations and all three
  review modes; invalid declarations, single-call approve/deny/cancel, exact
  argument retention, no saved-rule or temporary-directory exemption, and no
  command declaration override of native/MCP writes.
- Deliberately misleading declarations use mock execution. No destructive test
  command is dispatched. The elevated-command test also uses mock execution.
- `TestDeclaredReadLoopExecutesWithoutReview` executed an in-memory sum from 1
  through 100 and returned 5050 with PowerShell 7.4.14 and Windows PowerShell
  5.1.22621.7649. For the latter, directories containing `pwsh.exe` were removed
  from the test process PATH so the normal executor used its fallback host.
- `TestCommandToolsDeclareEffectInSchemaAndGuidance` passed after strengthening
  invalid-effect fixtures to supply the other required command arguments.
- Linux amd64 and macOS arm64: `go build ./...` and `go test -c ./internal/app`
  passed. These are compile checks only; their test binaries were not executed.
- `git diff --check` passed for the implementation and documentation. When the
  historical Windows P5 evidence was staged, its raw `.log` files reported
  whitespace warnings; these logs were preserved unchanged. The staged check
  passed with only those historical logs excluded.

No manual terminal interaction, real UAC consent, race-detector run, or Unix
runtime test was performed. Native authorization tests remain in the ordinary
suite. These checks do not establish that a model always declares effects correctly.

## Real-model first-round smoke

Command:

```text
go test -tags integration ./internal/app -run '^TestDeepSeekCommandEffectFirstRoundIntegration$' -count=1 -v
```

The test uses production prompt assembly and tool definitions with the DeepSeek
endpoint and requested model `deepseek-v4-flash`. The response model identity was
not independently verified. Both normal and minimal mode passed six cases each:
read a file, in-memory aggregation, create a file, build an artifact, Git push,
and execute a script with unknown contents. The first two required `read_only`;
the remaining four required `may_write`.

All 12 cases passed. [model-first-round.log](model-first-round.log) records the
first-response calls. There is no tool caller and the test never invokes
`CallTools`; generated commands were not executed. This is declaration smoke
evidence, not a task-completion or classification-accuracy benchmark. Test logs
are stored as UTF-8 with LF line endings.

## Retained history

`catalog_legacy.json` is the former immutable command-analysis catalog, copied
from HEAD before removing its package. It is historical data, not an active
policy or acceptance test. Existing app command-analysis snapshots remain in
their historical testdata directory.

The preexisting modified/untracked Windows P5 test sources were preserved
byte-for-byte in the neighboring
`2026-10-28-command-generation-p5-windows/retired-tests/` directory before removing
their obsolete analyzer dependency. Historical observations were not replayed or
re-scored.

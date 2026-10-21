# Development

## Requirements

- Go 1.25.8 or newer.
- Optional: a model provider key for manual testing.
- Optional: [Task](https://taskfile.dev). The examples below use `go run` so a
  separate Task install is not required.

## Build and Install

```sh
git clone https://github.com/panjie/mods.git
cd mods

go run github.com/go-task/task/v3/cmd/task@v3.51.1 build    # bin/mods
go run github.com/go-task/task/v3/cmd/task@v3.51.1 install  # ~/.local/bin/mods
```

`install` honors `BINDIR`, `PREFIX`, `DESTDIR`, and `XDG_BIN_HOME`. The default
target is `$HOME/.local/bin/mods` (`mods.exe` on Windows).

## Checks

Before opening a pull request, run the same baseline as CI:

```sh
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

`check` runs `go build ./...`. `test` runs `go test ./...` in the root module.
To mirror CI exactly, including verbose output and coverage:

```sh
go run github.com/go-task/task/v3/cmd/task@v3.51.1 ci
```

If installed, lint with the repository's `.golangci.yml` v2 configuration:

```sh
golangci-lint run ./...
```

## Focused Tests

Run a specific package and test while iterating:

```sh
go test ./internal/app -run TestName -count=1
go test ./internal/buildtask -run TestInstallDir -count=1
```

Build-task install-path tests live in `internal/buildtask`; run them after
changing `Taskfile.yml` or install-path logic. The protocol golden file updates
with:

```sh
go test ./internal/proto -run TestStringer -update
```

Provider integration tests are excluded by default. Run one with the matching
key, for example:

```sh
go test -tags integration ./internal/app -run TestOpenAIIntegration -count=1
```

Ollama integration uses `OLLAMA_HOST` or `http://localhost:11434` and the
`llama3.1` model.

## Real-AI Black-Box Tests

CLI black-box scenarios are kept behind the `integration` build tag and are not
part of the default CI suite. They build and execute the `mods` binary with
isolated config, data, cache, home, and working directories:

```sh
export DEEPSEEK_API_KEY=...
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test-blackbox-ai
```

These scenarios make paid network requests and cover structured pipe input,
session continuation across processes, complex read-only shell pipelines,
filesystem tool use, and absolute-path reads outside the current directory.

The runner uses the first available key in this provider order: DeepSeek, Qwen,
OpenAI, Anthropic, GLM, then Google. Google runs the non-tool scenarios because
its adapter does not expose built-in filesystem or shell tools.

Set `MODS_BLACKBOX_PROVIDER` to select a provider explicitly and
`MODS_BLACKBOX_MODEL` to override its default model:

```sh
MODS_BLACKBOX_PROVIDER=qwen MODS_BLACKBOX_MODEL=qwen-plus \
  go run github.com/go-task/task/v3/cmd/task@v3.51.1 test-blackbox-ai
```

## Windows Reliability

Windows contributors can run the layered [real-machine reliability suite](windows-reliability.md),
which validates PowerShell 5.1/7, Job Object process-tree cleanup, Windows path
boundaries, command shims, and output encodings while producing an auditable
report. It runs on Windows only:

```sh
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test-windows-reliability-quick
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test-windows-reliability-stress
```

## Windows UAC

Windows UAC tests run real named-pipe and helper subprocesses without displaying
consent dialogs in the default suite:

```sh
go test ./internal/tools -run TestElevation -count=1
go test ./internal/app -run 'TestElevat' -count=1
```

To verify the native UAC launch on a Windows desktop, run this from a
non-administrator PowerShell and accept the Windows consent dialog:

```powershell
$env:MODS_TEST_UAC = '1'
try { go test ./internal/tools -run '^TestUACIntegration$' -count=1 -v }
finally { Remove-Item Env:MODS_TEST_UAC }
```

This opt-in test verifies the elevated token, Unicode stdout, stderr, and an
intentional exit code of 7. It does not change system settings. UAC cancellation
produces a test failure, not an automatic retry. Running it from an already
elevated terminal tests inherited privileges instead of the UAC launch.

## Design Notes

Major subsystem design documents live in `docs/superpowers/specs/` and
`docs/superpowers/plans/`. Project-specific agent instructions and architecture
notes live in `AGENTS.md`.

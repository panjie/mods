# Task wrapper unavailable

Date: 2026-10-27. Environment: Linux amd64, Go 1.27.3.

## What failed

```text
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
```

failed while compiling the Task tool's own dependency graph:

```text
# google.golang.org/grpc/internal/transport
../../go/pkg/mod/google.golang.org/grpc@v1.79.3/internal/transport/handler_server.go:271:18: undefined: http2.TrailerPrefix
```

This is an upstream tool/module incompatibility with the installed Go 1.27.3
toolchain, not a failure of the P5 source or its tests. The project itself
uses `google.golang.org/grpc v1.64.1`; `go build ./...` and `go test ./...`
pass.

## Retries

- With the default read-only module cache, an explicit
  `GOTOOLCHAIN=go1.26.3` retry stopped at
  `mkdir /home/panjie/go/pkg/mod/cache/download/golang.org/toolchain: read-only file system`.
- With writable temporary `GOPATH`/`GOMODCACHE`/`GOCACHE` and
  `GOTOOLCHAIN=go1.26.3`, the command only printed
  `go: downloading go1.26.3 (linux/amd64)` (saved as `task-check.log`). It made
  no further progress and was canceled; no `task-test.log` exists.

## Equivalent commands actually verified

`Taskfile.yml` defines:

```text
check: go build ./...
test:  go test ./...
```

Both were run directly and passed:

- `verification/build.log` (empty output, exit 0)
- `verification/go-test.log` (all root-module packages pass)

Do not describe the Task wrapper itself as passing in this environment.

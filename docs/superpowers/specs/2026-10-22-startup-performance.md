# Startup performance

## Scope and invariants

The CLI parses arguments before opening SQLite. Configuration actions and
non-session listings need no database. A fresh `--no-save` request with existing
settings also skips storage, while continue/list and first-run history detection
still open it. `Ensure` resolves session paths without creating session storage.
Session completion creates storage only when it actually needs the DB.

`schemaVersion`/SQLite `user_version` gate the existing guarded migrations.
Version zero takes the legacy migration path; success records the current version
last. Current databases require only connection setup, foreign-key configuration
and the version read. Future versions fail explicitly. Schema changes must bump
`schemaVersion` and retain the migration guards/legacy fixtures.

Interactive session lookup runs alongside terminal probing. Its result, including
errors, is held until the background reply or 250 ms fallback. Early failures
therefore cannot leave terminal replies in the shell. The fallback itself is not
removed. Raw output does not allocate a Markdown renderer until explicitly asked
to render Markdown; unchanged background colours reuse an existing renderer.

Self-help catalog generation is lazy. Built-in catalog registration uses the same
tool table, but skips filesystem probes and replaces executable callbacks with
errors. Runtime registries keep ordinary path checks. Skill catalog scans read
frontmatter (at most 1 MiB), not instruction bodies. A bounded 1024-entry metadata
cache checks file identity, size and mtime; directories are still enumerated to
observe additions/removals. Bodies are read at `load_skill` time; renames require
rediscovery. As with other stat-based caches, an in-place metadata edit preserving
both size and mtime may remain cached until process restart. Full-content scan
APIs remain available.

`--chat` owns an MCP pool through context, closing it on chat exit. Only transport
sessions are reused, never request registries, approval rules or interaction
handlers. A hash of server configuration and subprocess environment invalidates
changed connections. Each reused session refreshes tool definitions/annotations
before registration, including read-only hints. Refresh failures reconnect;
tool-call transport errors invalidate the session for the next acquisition,
without replaying the failed call. Concurrent acquisitions use independent
sessions. Initial discovery remains cancellable by the requesting turn; retained
transports outlive that turn. Ordinary one-turn cleanup remains unchanged.

## Observability

`--debug` emits durations for configuration, database, app construction, terminal
probing, session lookup, provider setup, context assembly, tools, each new MCP
connection, request readiness and the first provider chunk. `request-ready`
measures request preparation, while `first-response` starts immediately before
`client.Request`. It includes connection/stream setup but does not measure visible
answer-token latency. Durations overlap and should not all be added together.

## Measurements

Linux amd64, AMD Ryzen AI MAX+ 395. Both revisions used the same Go toolchain.
All configurations/session directories were temporary; no production provider was
called. These are local warm-cache measurements, not cold boot or network claims.

`go test ./internal/app -run '^$' -bench BenchmarkAppStartup -benchtime=300ms -count=3`
was run on the previous revision and the modified tree. Median values:

| App construction | Before | After | Allocated bytes before / after |
| --- | ---: | ---: | ---: |
| No skills, formatted | 47.7 us | 28.6 us | 131,334 / 92,194 |
| No skills, raw | 47.7 us | 15.5 us | 131,336 / 52,613 |
| 100 skills, formatted | 13.84 ms | 0.253 ms | 56,000,016 / 246,351 |
| 100 skills, raw | 13.82 ms | 0.243 ms | 56,000,012 / 206,820 |

The skill fixture has about 130 KB of instructions per skill. Benchmarks repeatedly
construct the app within a process, exercising metadata cache reuse, and exclude
package initialization, terminal IO and provider/MCP network latency.

A separate single-iteration run (`-benchtime=1x -count=1`) selecting only
`skills=100/raw=true`, with an empty process-local metadata cache, measured
13.53 ms / 56.07 MB before and 0.862 ms / 1.09 MB after. This is a single
sample with warm filesystem pages, not a statistically robust cold-disk result.

Alternating before/after binaries, three warmups and 40 samples per command,
median process durations including subprocess creation:

| CLI invocation | Before | After |
| --- | ---: | ---: |
| `--version` | 10.23 ms | 10.49 ms |
| `--help` | 11.69 ms | 11.91 ms |
| `--dirs` | 11.39 ms | 10.92 ms |
| `--list-roles` | 11.35 ms | 10.73 ms |
| `--list-tools` | 11.68 ms | 11.12 ms |
| `--list-sessions --raw` | 11.67 ms | 11.66 ms |

These small CLI differences are sensitive to scheduler/cache noise. In particular
help/version and session-list end-to-end performance is essentially unchanged.

## Remaining dependency initialization

The existing Chroma v2.14.0 eagerly parses embedded lexer/style XML before main.
The source of downloaded v2.27.0 still does so in `lexers/lexers.go` and
`styles/api.go`; a direct version bump cannot fix that cost. No dependency bump,
local fork, renderer migration or syntax-highlighting removal is included here.
Such a change requires a separate dependency-maintenance decision.

## Validation

- `go build ./...` and `go test ./...` passed.
- Race tests passed for app, CLI, session, skills, self-help, tools, tooling and MCP.
- Regressions cover absent session storage for non-session actions, read-only
  reopen of current schemas, future-version rejection, both terminal/session
  arrival orders, delayed startup errors, renderer reuse, lazy skill bodies,
  metadata invalidation, inert tool catalogs, MCP permission refresh, connection
  reuse/replacement and cancellation cleanup.
- The prescribed Task v3.51.1 wrapper failed to compile its own grpc/http2
  dependency (`undefined: http2.TrailerPrefix`). The equivalent root build and
  test commands above were executed directly.

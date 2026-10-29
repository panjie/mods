# Long-conversation context management handoff

## Current state

Uncommitted implementation on top of 58c2f97. User resumed work after the earlier
capacity stop. Manual compaction, automatic pre-turn triggering, summary
persistence/reuse, incremental summarization and bounded history retrieval now
exist. Single-turn tool-loop compaction and chunked large-source summarization
remain future work; do not describe those as delivered.

## Implementation

- `internal/contextplan/plan.go`: complete older-prefix selection, retaining the
  current and previous user turns; fails closed on incomplete tool groups.
- `internal/app/context_compaction.go`: tool-free summaries; 256 KiB source cap,
  requested 2048 output tokens, 32 KiB output cap. Errors, empty output and tool
  requests do not replace original history. Summaries are conversational context,
  never system instructions or authorization.
- `Mods.compactedPrefix` / `compactedText`: originals separate from model view.
  `Messages()` restores originals for saving; retries retain the compact view.
  Fresh context resets these fields. Adapters must preserve input ordering.
- `--compact`: nonpersistent manual trigger. `context-max-bytes` / matching flag
  and environment variable: opt-in automatic trigger before turns; zero disables.
  This measures conversation JSON bytes, not tokens or a hard request-size limit.
- `internal/session/context_summary.go`: versioned summary, covered non-system
  message count and SHA-256 fingerprint. Canonical JSON includes media and opaque
  provider state plus explicitly hashed thought signatures. Source edits invalidate
  the summary. Matching coverage is reused without a model request; new coverage
  summarizes the old summary plus newly covered records.
- Schema v2 adds context_summaries with ON DELETE CASCADE. SaveSession accepts an
  optional summary and saves it in the same transaction as full history/rules.
  Saving without a summary clears the prior summary. Invalid summaries roll back
  all changes. Old v1 databases migrate through the guarded migration path.
- `history_read`: registered once in tooling.builtinTools, enabled only for an
  archived prefix. Read-only, invocation-local, 1-based message index and bounded
  UTF-8 byte pagination. Returns text fragments, counts and next offsets. No raw
  media, private provider state, other sessions or authorization capabilities.
- README documents usage, persistence, triggering and current limits.

## Verification

Tests cover five local protocol families (Chat Completions, Responses, Anthropic,
Google, Ollama), original transcript preservation, failures, no summary tool
execution, retries, fresh-model reset, persisted cache reuse and incremental
summary requests. Additional tests cover source bounds, automatic threshold,
DB reopen, v1 migration, stale summary invalidation, transaction rollback, media
and signature fingerprints, history pagination and isolation.

Build check and the final full root-module test suite both passed.
No live/paid provider tests were run. No commit or background automation created.

## Remaining limitations / follow-up

1. Automatic compaction only runs before user turns. Each adapter currently owns
   follow-up model requests within a tool loop. A provider-neutral completed-round
   hook and a multiple-compaction transcript mapping are needed for long single
   turns. Never replay completed writes.
2. Sources over 256 KiB fail explicitly. Add bounded chunked summarization with
   all-or-nothing application, cancellation and cost accounting. A current prompt
   or recent turns can themselves exceed a model window; threshold is not a cap.
3. Prefix counts RoleUser messages. Verify synthetic media projections remain
   wire-only before adding adapter/turn formats that could affect boundaries.
4. Original media remains saved; history_read supplies labels only. Any future
   original-media retrieval needs explicit shared budgets and truthful uncertainty.
5. Add cancellation/empty/truncated-summary/live-provider tests and real CLI/chat
   end-to-end tests. Summarization correctness itself is not proven by mock text.
6. No slash command exists. If adding one, define how to send literal slash text.
7. Generic transcript restoration assumes the synthetic summary is the first
   non-system message; five-protocol fixtures protect this but arbitrary history
   editing will require explicit stable message identities.

## Commands

```sh
go test ./internal/contextplan -count=1
go test ./internal/app -run 'TestManualCompaction|TestCompactionSourceLimit|TestAutomaticContext' -count=1
go test ./internal/session -run TestContextSummary -count=1
go test ./internal/tools -run TestHistoryRead -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

Read AGENTS.md before continuing. Keep summary storage separate from approval
rules and never persist the invocation-only Full control choice.

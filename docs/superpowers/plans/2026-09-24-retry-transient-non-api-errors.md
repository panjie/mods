# Retry Transient Non-API Errors Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transient non-API request failures (truncated provider bodies, connection resets) are retried through the existing `m.retry` path instead of killing the turn immediately.

**Architecture:** `handleRequestError`'s non-API fallback (internal/app/mods_errors.go) gains a blacklist classifier `isTransientRequestError` — everything retries except `context.Canceled`/`context.DeadlineExceeded`. Retry reuses the existing `retryMsg` → `completionInput` → `startCompletionCmd` flow (whole-turn restart, same semantics as 429/503 retries today). A one-line fix resets `m.retries` at successful turn end so chat mode does not exhaust the retry budget across turns.

**Tech Stack:** Go, Bubble Tea (charm.land/bubbletea/v2), testify (stretchr).

**Spec:** Diagnosis from the 2026-09-24 debug-log analysis session (this conversation): `unexpected end of JSON input` from mi provider (empty/truncated HTTP body parsed by openai-go SDK) fell into the non-API branch of `handleRequestError` (internal/app/mods_errors.go:42-49) and returned a fatal `modsError`; `retry()` was only reachable from `dispatchAPIError` (HTTP errors). No separate spec doc.

## Global Constraints

- Go module `github.com/panjie/mods`; LF line endings; `gofmt`/`goimports` formatting.
- No comments unless requested (repo AGENTS.md style); doc comments on exported identifiers only. `isTransientRequestError` is unexported and carries no doc comment per repo style.
- No commits unless the user explicitly asks.
- Tests use `testify/require`, `testMods(t)` helper from mods_errors_test.go.

## Verified Facts (from research)

- `errh` is only invoked from `streamEventError` (internal/app/mods.go:401); all errors reaching it are provider-request I/O/parse failures. No user-config errors flow through it.
- `m.retry(content, modsError)` increments `m.retries`, gives up at `m.Config.MaxRetries` (default 5, config_template.yml:72), returns `retryMsg{content, wait}` with exponential backoff.
- Mid-turn retry restarts the whole turn from the user input (`setupStreamContext` resets `m.messages`, internal/app/stream.go:24) — existing 429/503 semantics; tool calls re-run; session approval rules persist.
- `m.retries` is never reset in production code (only tests set it to 0) — chat-mode budget accumulation quirk, also affects 429/503 today.
- `debugTurnActive` is not reset when debug is disabled (debug_lifecycle.go:53 early return), so it cannot serve as a "new turn" detector; reset belongs at successful turn end (`streamEventDone`).
- Write-target inference failures are already tolerated (write_targets.go:44-48) — out of scope.
- Existing test `non-openai error returns generic message` (mods_errors_test.go:182-190) expects `modsError` for `network timeout`; it must be updated to an exhausted-budget setup because fresh-budget transient errors now retry.

## Review Focus

- Ctrl+C during a request: the stream error must not trigger a retry (covered by context.Canceled exclusion, including wrapped).
- Deterministic failures (bad proxy/TLS config) burn the full budget (MaxRetries=5 → 4 retries, 0.2+0.4+0.8+1.6 = 3.0s total backoff) before failing — acceptable, but UX reviewers should know.
- Retry restarts the turn and re-runs completed tool calls — pre-existing semantics, not introduced here (mid-stream truncation makes it practically reachable; follow-up: resume from last good assistant/tool state or surface a "restarting turn" notice).
- Chat mode: budget must recover after a successful turn (Task 2 — NOTE post-review: chat turns create a fresh Mods each time (cli.runOneTurn), so this is defense-in-depth, not a live bug fix).

---

### Task 1: Retry transient non-API errors

**Files:**
- Modify: `internal/app/mods_errors.go:42-49`
- Test: `internal/app/mods_errors_test.go`

**Interfaces:**
- Consumes: `m.retry(content string, err modsError) tea.Msg` (internal/app/completion.go:28), `retryMsg` (internal/app/messages.go:39).
- Produces: `isTransientRequestError(err error) bool` (unexported, internal/app/mods_errors.go).

- [ ] **Step 1: Write the failing test** — new top-level test function in mods_errors_test.go (imports: add `"context"`, `"errors"`):

```go
func TestHandleRequestErrorRetriesTransientNonAPIErrors(t *testing.T) {
	mod := Model{Name: "mimo-v2.6-flash", API: "mi"}

	t.Run("truncated JSON body retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 5
		m.retries = 0
		msg := m.handleRequestError(errors.New("unexpected end of JSON input"), mod, "prompt")
		rm, ok := msg.(retryMsg)
		require.True(t, ok, "expected retryMsg, got %T", msg)
		require.Equal(t, "prompt", rm.content)
	})

	t.Run("connection reset retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 5
		m.retries = 0
		msg := m.handleRequestError(fmt.Errorf(`Post "/v1/chat/completions": read tcp ...: connection reset by peer`), mod, "prompt")
		_, ok := msg.(retryMsg)
		require.True(t, ok, "expected retryMsg, got %T", msg)
	})

	t.Run("context canceled does not retry", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 5
		m.retries = 0
		msg := m.handleRequestError(context.Canceled, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok, "expected modsError, got %T", msg)
		require.Contains(t, merr.ReasonText, "mi API request")
	})

	t.Run("wrapped context cancel does not retry", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 5
		m.retries = 0
		msg := m.handleRequestError(fmt.Errorf("stream: %w", context.Canceled), mod, "prompt")
		_, ok := msg.(modsError)
		require.True(t, ok, "expected modsError, got %T", msg)
	})

	t.Run("context deadline does not retry", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 5
		m.retries = 0
		msg := m.handleRequestError(context.DeadlineExceeded, mod, "prompt")
		_, ok := msg.(modsError)
		require.True(t, ok, "expected modsError, got %T", msg)
	})

	t.Run("retries exhausted returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 2
		msg := m.handleRequestError(errors.New("unexpected end of JSON input"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "mi API request")
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestHandleRequestErrorRetriesTransientNonAPIErrors -count=1`
Expected: FAIL (first two subtests compile-time fine but assert retryMsg; implementation returns modsError).

- [ ] **Step 3: Implement** — internal/app/mods_errors.go: replace the non-API fallback with:

```go
	debug.Printf("Request error (non-API): %v", err)
	me := modsError{
		Err: err,
		ReasonText: fmt.Sprintf(
			"There was a problem with the %s API request.",
			mod.API,
		),
	}
	if isTransientRequestError(err) {
		return m.retry(content, me)
	}
	return me
```

Add below `handleRequestError` (imports: add `"context"`):

```go
func isTransientRequestError(err error) bool {
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
```

- [ ] **Step 4: Update affected existing test** — mods_errors_test.go:182-190 `non-openai error returns generic message`: set `m.Config.MaxRetries = 1; m.retries = 1` so the terminal path is exercised.

```go
	t.Run("non-openai error returns generic message", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 1
		m.retries = 1
		otherErr := fmt.Errorf("network timeout")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleRequestError(otherErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request")
	})
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/app -run TestHandleRequestError -count=1`
Expected: PASS (all subtests).

### Task 2: Reset retry budget at successful turn end

**Files:**
- Modify: `internal/app/mods.go:387` (`case streamEventDone:` branch, before `usage := msg.runner.takeUsage()`)
- Test: `internal/app/completion_test.go`

**Interfaces:**
- Consumes: `streamEventMsg{kind: streamEventDone, runner: r}` Update path (internal/app/mods.go:387-398).
- Produces: none (behavior only).

- [ ] **Step 1: Write the failing test** — completion_test.go, style follows TestUpdateSchedulesRetryTick (m := testMods(t); drive m.Update(...); assert m.retries == 0). Use a runner from newStreamRunner(nil, nil, nil, nil) — streamEventDone path needs runner only for takeUsage (nil-safe per stream_runner.go:89-97).

```go
func TestRetryBudgetResetsAfterSuccessfulTurn(t *testing.T) {
	m := testMods(t)
	m.Config.MaxRetries = 5
	m.retries = 5
	_, cmd := m.Update(streamEventMsg{kind: streamEventDone, runner: newStreamRunner(nil, nil, nil, nil)})
	require.NotNil(t, cmd, "done turn schedules quit")
	require.Equal(t, 0, m.retries, "retry budget must reset after a successful turn")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestRetryBudgetResetsAfterSuccessfulTurn -count=1`
Expected: FAIL (`m.retries` stays 5).

- [ ] **Step 3: Implement** — internal/app/mods.go `case streamEventDone:` first line: `m.retries = 0`.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/app -run 'TestRetryBudgetResetsAfterSuccessfulTurn|TestRetry' -count=1`
Expected: PASS.

### Final verification

- [ ] `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`
- [ ] `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test`
- [ ] Final whole-branch review (fresh subagent, per executing-plans).

# Pipe Mode Approval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow required tool approvals when prompt input is piped and a real terminal is still available for approval.

**Architecture:** Keep `os.Stdin` dedicated to pipe content, probe the controlling terminal in pipe mode, and use Bubble Tea's TTY input support for approval keys only when that probe succeeds. Carry a runtime-only `InteractiveReviewAvailable` flag from CLI setup into the app reviewer so review policy matches the actual input path.

**Tech Stack:** Go, Bubble Tea `tea.WithInputTTY`, existing `internal/config`, `internal/cli`, and `internal/app` review policy.

## Global Constraints

- Do not let piped stdin bytes approve tool execution.
- Do not add a user-facing flag or persisted config key.
- Raw mode remains non-interactive.
- Approval UI must write to stderr, not stdout.
- Existing saved-rule and access-matrix behavior remains unchanged.
- Secret approval remains unpersistable and requires interactive review availability.

---

### Task 1: Runtime Review Availability

**Files:**
- Modify: `internal/config/config.go:268-277`
- Modify: `internal/cli/run.go:21-31`
- Modify: `internal/app/review.go:62-67,309-350,397-400`
- Test: `internal/cli/main_test.go`
- Test: `internal/app/approval_rules_test.go`

**Interfaces:**
- Consumes: `ui.IsInputTTY`, `ui.IsErrorTTY`, a small controlling-terminal probe, Bubble Tea `tea.WithInputTTY`.
- Produces: `Config.InteractiveReviewAvailable bool`, `toolReviewer.canReviewInteractively() bool`.

- [ ] **Step 1: Write failing CLI tests**

Add tests to `internal/cli/main_test.go`:

```go
func TestBuildTeaProgramOptionsMarksPipeReviewAvailableWhenStderrTTY(t *testing.T) {
	savedConfig := config
	savedIsInputTTY := IsInputTTY
	savedIsOutputTTY := IsOutputTTY
	savedIsErrorTTY := IsErrorTTY
	t.Cleanup(func() {
		config = savedConfig
		IsInputTTY = savedIsInputTTY
		IsOutputTTY = savedIsOutputTTY
		IsErrorTTY = savedIsErrorTTY
	})

	config = Config{}
	IsInputTTY = func() bool { return false }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return true }

	_ = buildTeaProgramOptions()

	require.True(t, config.InteractiveReviewAvailable)
}

func TestBuildTeaProgramOptionsDoesNotMarkRawPipeReviewAvailable(t *testing.T) {
	savedConfig := config
	savedIsInputTTY := IsInputTTY
	savedIsOutputTTY := IsOutputTTY
	savedIsErrorTTY := IsErrorTTY
	t.Cleanup(func() {
		config = savedConfig
		IsInputTTY = savedIsInputTTY
		IsOutputTTY = savedIsOutputTTY
		IsErrorTTY = savedIsErrorTTY
	})

	config = Config{PersistentConfig: PersistentConfig{Raw: true}}
	IsInputTTY = func() bool { return false }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return true }

	_ = buildTeaProgramOptions()

	require.False(t, config.InteractiveReviewAvailable)
}
```

- [ ] **Step 2: Run CLI tests and verify RED**

Run: `go test ./internal/cli -run "TestBuildTeaProgramOptions.*ReviewAvailable" -count=1`

Expected: FAIL to compile because `Config.InteractiveReviewAvailable` does not exist yet.

- [ ] **Step 3: Write failing app tests**

Add tests to `internal/app/approval_rules_test.go` near `TestReviewPolicyNonTTY`:

```go
func TestRequestApprovalUsesInteractiveReviewAvailability(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	registry := testReviewRegistry(t)
	mods := &Mods{
		ctx:                 context.Background(),
		Config:              testConfigForWorkspace(testApprovalScope.Value),
		currentToolRegistry: registry,
	}
	mods.Config.InteractiveReviewAvailable = true
	reviewer := newToolReviewer(mods.Config)
	reviewer.reviewChan = make(chan toolReviewItem, 1)

	errCh := make(chan error, 1)
	go func() {
		errCh <- reviewer.requestApproval(testReviewerDeps(mods), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
	}()

	item := receiveReviewItem(t, reviewer.reviewChan)
	require.Equal(t, "fs_write_file", item.name)
	item.resp <- reviewResponse{approved: true}
	require.NoError(t, <-errCh)
}

func TestRequestApprovalRawModeIgnoresInteractiveReviewAvailability(t *testing.T) {
	oldIsInputTTY := IsInputTTY
	IsInputTTY = func() bool { return false }
	t.Cleanup(func() { IsInputTTY = oldIsInputTTY })

	cfg := testConfigForWorkspace(testApprovalScope.Value)
	cfg.Raw = true
	cfg.InteractiveReviewAvailable = true
	reviewer := newToolReviewer(cfg)

	err := reviewer.requestApproval(
		reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{Class: AccessWrite}},
		"fs_write_file",
		[]byte(`{"path":"out.txt","content":"x"}`),
	)
	require.ErrorIs(t, err, errReviewUnavailable)
}
```

- [ ] **Step 4: Run app tests and verify RED**

Run: `go test ./internal/app -run "TestRequestApproval.*InteractiveReviewAvailability" -count=1`

Expected: FAIL to compile because `Config.InteractiveReviewAvailable` and reviewer support do not exist yet.

- [ ] **Step 5: Implement minimal runtime config field**

In `internal/config/config.go`, add this runtime-only field:

```go
	InteractiveReviewAvailable bool
```

Place it with the other runtime state fields so it is not persisted.

- [ ] **Step 6: Implement minimal CLI option selection**

In `internal/cli/run.go`, add a small `canOpenReviewTTY` probe and change `buildTeaProgramOptions()` so raw mode or non-TTY stderr disables review input, stdin TTY plus stderr TTY marks review available, and pipe mode uses Bubble Tea's TTY input only after the probe succeeds:

```go
var canOpenReviewTTY = func() bool {
	path := "/dev/tty"
	flag := os.O_RDONLY
	if runtime.GOOS == "windows" {
		path = "CONIN$"
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		debug.Printf("Review TTY unavailable: %v", err)
		return false
	}
	if err := f.Close(); err != nil {
		debug.Printf("Review TTY close failed: %v", err)
	}
	return true
}

func buildTeaProgramOptions() []tea.ProgramOption {
	opts := []tea.ProgramOption{}
	config.InteractiveReviewAvailable = false

	if config.Raw || !IsErrorTTY() {
		opts = append(opts, tea.WithInput(nil))
	} else if IsInputTTY() {
		config.InteractiveReviewAvailable = true
	} else if canOpenReviewTTY() {
		opts = append(opts, tea.WithInputTTY())
		config.InteractiveReviewAvailable = true
	} else {
		opts = append(opts, tea.WithInput(nil))
	}

	if IsErrorTTY() && !config.Raw {
		opts = append(opts, tea.WithOutput(os.Stderr))
	} else {
		opts = append(opts, tea.WithoutRenderer())
	}
	return opts
}
```

- [ ] **Step 7: Implement reviewer availability predicate**

In `internal/app/review.go`, add a field and helper:

```go
type toolReviewer struct {
	// existing fields...
	raw                        bool
	reviewAvailabilityKnown    bool
	interactiveReviewAvailable bool
}

func (r *toolReviewer) canReviewInteractively() bool {
	if r.raw {
		return false
	}
	if r.reviewAvailabilityKnown {
		return r.interactiveReviewAvailable
	}
	return IsInputTTY()
}
```

In `newToolReviewer`, set:

```go
		raw:                        cfg.Raw,
		reviewAvailabilityKnown:    true,
		interactiveReviewAvailable: !cfg.Raw && cfg.InteractiveReviewAvailable,
```

Replace the `!IsInputTTY()` checks in `requestApproval` and `requestSecretApproval` with `!r.canReviewInteractively()`.

- [ ] **Step 8: Run focused tests and verify GREEN**

Run:

```bash
go test ./internal/cli -run "TestBuildTeaProgramOptions.*ReviewAvailable" -count=1
go test ./internal/app -run "TestRequestApproval.*InteractiveReviewAvailability|TestRequestApprovalRawTTYModeDoesNotWaitForReview|TestRequestApprovalTTYInputWithoutReviewUIIsUnavailable|TestReviewPolicyNonTTY|TestViewShowsReviewBannerWhenStdoutIsNotTTYButReviewInputIsAvailable" -count=1
```

Expected: PASS.

- [ ] **Step 9: Run broader verification**

Run:

```bash
go test ./internal/cli ./internal/app -count=1
```

Expected: PASS.

- [ ] **Step 10: Review diff for simplicity**

Check:

```bash
git diff -- internal/config/config.go internal/cli/run.go internal/cli/main_test.go internal/app/review.go internal/app/approval_rules_test.go docs/superpowers/specs/2026-07-13-pipe-mode-approval-design.md docs/superpowers/plans/2026-07-13-pipe-mode-approval.md
```

Expected: only the runtime flag, minimal CLI option selection, minimal reviewer predicate, and focused tests changed.

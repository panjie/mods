# Pipe Mode Approval Design

## Goal

Allow tool approvals while `mods` is reading prompt content from a pipe, without letting piped data approve tool execution and without adding user-facing configuration.

## Current Behavior

- `internal/app/session_ops.go` reads `os.Stdin` as prompt content when stdin is not a TTY.
- `internal/cli/run.go` disables Bubble Tea input in that case with `tea.WithInput(nil)`.
- `internal/app/review.go` rejects required approvals when stdin is not a TTY.

This is safe but prevents a common interactive use case: `cat file | mods ...` can still have a real terminal available for approval on stderr/console.

## Design

Keep pipe content and approval input separate:

- `os.Stdin` remains the only source for piped prompt content.
- In pipe mode, when `stderr` is a TTY and raw mode is off, first verify the controlling terminal can be opened for approval input:
  - Unix: `/dev/tty`
  - Windows: `CONIN$`
- If that probe succeeds, Bubble Tea uses its TTY input mode for approval keys instead of `os.Stdin`.
- UI output remains on `stderr`, preserving clean `stdout` output.
- If no controlling terminal can be opened, the current fail-closed behavior remains.
- Raw mode remains non-interactive and does not enable approval UI.
- Secret approval continues to require a real interactive approval channel and remains unpersistable.

## Code Boundaries

- Add a small terminal-input probe helper near the CLI Bubble Tea option setup.
- Update `buildTeaProgramOptions()` to choose approval input in pipe mode only when safe.
- Replace approval availability checks in `internal/app/review.go` with a shared predicate that reflects whether Bubble Tea can receive approval keys.
- Keep existing saved-rule and access-matrix behavior unchanged.

## Error Handling

If a tool requires approval and no interactive approval channel is available, keep returning the existing `errReviewUnavailable` style error. The message should continue telling users to run interactively or intentionally use `--review-mode never` for non-interactive execution.

## Testing

Add focused tests for the policy and option selection where practical:

- Pipe mode without an approval terminal still denies required approval.
- Pipe mode with an approval terminal is considered review-capable and does not fail before queuing review.
- Raw mode stays review-unavailable.
- Existing TTY mode behavior remains unchanged.

Avoid full end-to-end terminal tests unless necessary; this feature should stay small and testable through helper functions and reviewer behavior.

# Command reviewability design

Updated 2026-09-10: enforced preflight and paginated review. There is no general
script execution tool; see
[implementation plan](../plans/2026-09-10-cross-platform-command-reviewability.md).

## Goal

Make model-generated command calls small enough for a human to review without
removing the ability to execute legitimate shell pipelines.

Reviewability is independent of safety. A complex command can be read-only,
and a simple command can mutate external state. Existing access intent,
directory approval, dynamic-target, and secret approval rules still authorize
effects, after the independent structural execution constraint passes.

## Layers

1. Capability-aware system guidance prefers `process_run` for one executable
   and reserves shell tools for actual shell syntax.
2. The command's deterministic `CommandAssessment` reports structural
   reviewability facts without a second parse or an LLM call.
3. Every call passes a deterministic execution constraint. Up to two corrections
   are allowed per request; exhaustion rejects the call instead of permitting it.
4. Structured downloads carry explicit URL/path lists. Ordinary shell/process
   reviews paginate long content and escape terminal controls. General scripts
   are not an execution path: opaque or interpreter-wrapped content must be
   decomposed into separate single-purpose calls.

## Assessment dimension

`CommandReviewability` reports a level (`simple`, `compound`, or `opaque`),
stable reason codes, an optional recommended tool, and a narrow
`ShouldCorrect` decision. It is only one dimension of `CommandAssessment`.
Parser-derived action and pipeline counts live in `CommandShape`; runtime path
expressions live in `CommandAssessment.DynamicTargets`. Those fields drive
correction messages, while the review UI stays focused on the operation and
affected target.

POSIX analysis uses the same mvdan AST as effect and path analysis. PowerShell
analysis uses the same bridge IR as effect and dynamic-target analysis. Parse
failure produces an opaque, unknown assessment; unknown effects fail closed.

Pipelines count as one purpose. Semicolon-separated or conditional branches
count as separate actions. Literal output decoration is advisory and cannot by
itself cause a correction.

## Correction boundary

The preflight may correct a single executable wrapped in shell, mixed
inspection and mutation, a dynamic write target, or multiple top-level actions.
The separate RequiresSimplification decision enforces structural constraints;
ShouldCorrect remains advisory metadata. It never
rewrites or executes the source.

The gate is local to one request and protected for parallel calls. Advisory
single-program tool selection is suggested once independently of the hard
correction budget. Compound non-proven-read calls, opaque interpreter content
and unresolved write targets cannot use ordinary approval. Static reads remain
exempt; an LLM read verdict cannot remove structural rejection. The first two
rejected calls return correction guidance; later unreviewable calls are
rejected as ordinary tool failures, and changing the tool name or payload does
not reset the budget. Exhaustion does not end the turn: the rejected call never
runs, the model continues with the rejection as feedback, and the next user
request gets a fresh budget. Minimal mode retains the constraint; explicit
review-never bypasses it.

Opaque or interpreter-wrapped content never receives ordinary approval; the
correction feedback requires separate literal single-purpose calls and rejects
hiding code in interpreter flags, temporary files, or encoded arguments.

## Script execution release valve

Updated 2026-09-11. An interpreter invocation stays opaque, but one shape can be
reviewed honestly instead of being rejected: a whole command that is a single
bare interpreter with exactly one literal script-path operand.

- Detection lives in `internal/approval/script_exec.go` and keeps the command
  opaque (`ReviewabilityScriptExecution` plus a candidate
  `ScriptExecutionFacts`). Inline `-c`/`-e`, module flags, extra arguments,
  encoded payloads, nested shell hosts, pipelines, redirections, assignments,
  dynamic or glob operands, path-qualified interpreters, and external scripts
  stay opaque and are rejected exactly as before.
- Eligibility lives in the app layer (`internal/app/script_review.go`): the
  operand must resolve inside the workspace or a safe directory, survive symlink
  resolution, and be a readable, NUL-free, valid UTF-8 regular file of at most
  128 KiB. Otherwise the facts stay unverified and the preflight rejects the
  call.
- The effect is forced back to unknown. A classifier read verdict must not lift
  the payload into the always-allowed read cell, and a guessed target must not
  advertise a bounded scope.
- Review shows the resolved path, size, SHA-256, and the complete escaped source.
  Shell and process reviews are already paginated, so approval stays unavailable
  until every page has been displayed. `candidateRulesForIntent` offers no rule
  for the intent, and `requestApproval` skips the saved-rule shortcut for
  verified script calls: a path rule cannot speak for bytes that may change.
- Execution is bound to the displayed bytes: the reviewer refreshes the digest
  while rendering, and `request_session.go` re-reads the file immediately before
  the call and refuses to run a script that changed. The residual window between
  that check and the interpreter opening the file is accepted on purpose;
  executing a snapshot copy would change the interpreter's view of its own path.
- There is still no general script execution tool, and a script executed directly
  by path (`./tools/check.py`) or through a shell host remains opaque.

Correction messages describe structural facts but do not echo commands,
dynamic target expressions, secret references, or argument values.

## Non-goals

- Automatically split, rewrite, or reorder shell source.
- Infer semantic independence with an LLM.
- Treat complexity as mutation risk.
- Add persistent settings or database state.
- Infer all behavior of arbitrary native executables, imported scripts or remote services.

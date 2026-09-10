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

Correction messages describe structural facts but do not echo commands,
dynamic target expressions, secret references, or argument values.

## Non-goals

- Automatically split, rewrite, or reorder shell source.
- Infer semantic independence with an LLM.
- Treat complexity as mutation risk.
- Add persistent settings or database state.
- Infer all behavior of arbitrary native executables, imported scripts or remote services.

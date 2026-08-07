# Command reviewability design

## Goal

Make model-generated command calls small enough for a human to review without
removing the ability to execute legitimate shell pipelines or scripts.

Reviewability is independent of safety. A complex command can be read-only,
and a simple command can mutate external state. Existing access intent,
directory approval, dynamic-target, and secret approval rules remain the sole
authority for execution approval.

## Layers

1. Capability-aware system guidance prefers `process_run` for one executable
   and reserves shell tools for actual shell syntax.
2. The command's deterministic `CommandAssessment` reports structural
   reviewability facts without a second parse or an LLM call.
3. One bounded preflight correction per request gives the model an opportunity
   to split a needlessly compound call before approval.
4. Commands that still reach review display reviewability separately from risk.

## Assessment dimension

`CommandReviewability` reports a level (`simple`, `compound`, or `opaque`),
stable reason codes, an optional recommended tool, and a narrow
`ShouldCorrect` decision. It is only one dimension of `CommandAssessment`.
Parser-derived action and pipeline counts live in `CommandShape`; runtime path
expressions live in `CommandAssessment.DynamicTargets`. Those fields are the
single facts used by correction messages and the review UI.

POSIX analysis uses the same mvdan AST as effect and path analysis. PowerShell
analysis uses the same bridge IR as effect and dynamic-target analysis. Parse
failure produces an opaque, unknown assessment; unknown effects fail closed.

Pipelines count as one purpose. Semicolon-separated or conditional branches
count as separate actions. Literal output decoration is advisory and cannot by
itself cause a correction.

## Correction boundary

The preflight may correct a single executable wrapped in shell, mixed
inspection and mutation, a dynamic write target, or a compound call with three
or more top-level actions (or multiple actions plus a dynamic target). It never
rewrites or executes the source.

The gate is local to one request and protected for parallel tool calls. The
first qualifying approval-difficult call returns a typed retry suggestion.
Read-only single-program shell calls retain a `process_run` recommendation but
do not spend this budget; it is reserved for compound, mixed-effect, dynamic,
or mutating calls. Later calls proceed to normal review so a model cannot enter
an unbounded correction loop. Correction errors do not consume the consecutive
failed-tool budget. Minimal mode, plan mode, and review-never preserve their
prior semantics and bypass the gate.

Correction messages describe structural facts but do not echo commands,
dynamic target expressions, secret references, or argument values.

## Non-goals

- Automatically split, rewrite, or reorder shell source.
- Infer semantic independence with an LLM.
- Treat complexity as mutation risk.
- Add public tool arguments, persistent settings, or database state.
- Reject complex commands permanently.

# Command reviewability design

Updated 2026-09-10: enforced preflight and full script review. See
[implementation plan](../plans/2026-09-10-cross-platform-command-reviewability.md).

## Goal

Make model-generated command calls small enough for a human to review without
removing the ability to execute legitimate shell pipelines or scripts.

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
   are allowed per request; exhaustion stops execution instead of permitting it.
4. Structured downloads carry explicit URL/path lists. Necessary scripts use
   script_run with complete source and one-time, paginated review. Ordinary
   shell/process reviews also paginate long content and escape terminal controls.

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
correction budget. Compound non-proven-read calls, opaque scripts and unresolved
write targets cannot use ordinary approval. Static reads remain exempt; an LLM
read verdict cannot remove structural rejection. The third rejected call is a
terminal error, and changing the tool name or payload does not reset the budget.
Minimal mode retains the constraint; explicit review-never bypasses it.

script_run accepts up to 8192 bytes of readable sh, PowerShell, Python, Node or
Emacs source. The invocation source is held in tool arguments, so no script file
is reopened after approval. The interpreter path is resolved before review.
Unknown script effects cannot create saved rules or use temporary-write
exemption. Imported files and child processes are not frozen or sandboxed.

Correction messages describe structural facts but do not echo commands,
dynamic target expressions, secret references, or argument values.

## Non-goals

- Automatically split, rewrite, or reorder shell source.
- Infer semantic independence with an LLM.
- Treat complexity as mutation risk.
- Add persistent settings or database state.
- Infer all behavior of arbitrary native executables, imported scripts or remote services.

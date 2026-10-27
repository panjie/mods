# Command reviewability design

> P4 update (2026-10-27): the default remains diagnostic-only (`command-simplification: off`). Opt-in `once` adds a separate app-owned, request-scoped control result for narrowly qualified commands with unproven effects. It replaces the historical two-error simplification scheme, never grants permission, and retains ordinary approval for subsequent calls. See the [current organization design](2026-10-26-reviewable-command-generation-design.md) and [P4 implementation record](../evidence/2026-10-27-command-generation-p4/README.md).

> Historical policy notice (2026-10-25): any LLM effect-completion, read-classifier exemption, correction budget, or KnownDirs-based authorization described below is superseded. Production uses deterministic `commandanalysis.Engine` + the shared catalog, role-aware `app.commandResultToIntent`, and ordinary target approval. Unknown/incomplete writes cannot borrow saved or temporary-directory permissions. See [current design](2026-10-23-command-analysis-catalog-design.md) and [stage-6 verification](../plans/2026-10-25-command-analysis-stage6.md). Reviewability remains diagnostic metadata only.

Updated 2026-10-19: command structure is diagnostic metadata only. The former
simplification preflight and its two-error budget have been removed.

## Goal

Keep execution reviewable without requiring models to fragment related work or
rewrite already-correct commands. A command can be complex and read-only, or
simple and mutating. Authorization depends on effects and targets.

## Execution path

1. Validate arguments and assess the original invocation once.
2. Log structural diagnostics when `HasReviewabilityConcerns` is true. Log only
   structural facts: tool, level, action/pipeline counts, opacity, script-file
   payload, and reason codes. Do not include argument values or secret references.
3. Build the ordinary access intent and call `requestApproval`.
4. Execute the original arguments once when authorized, then return the real
   output and status. No complexity error, correction budget, or resubmission.

Static reads, classifier-completed reads, writes, saved target rules, unknown
effects, unavailable approval, user denial, and review modes keep their existing
access-policy behavior. Opaque syntax remains a structural fact even when the
classifier can establish a read. Structural diagnostics never authorize a call.

## Structural facts

`CommandReviewability` reports a level (`simple`, `compound`, or `opaque`), reason
codes, a recommended tool, and whether the payload is a literal script file.
`CommandShape` carries action/pipeline counts and opacity. POSIX uses the existing
mvdan AST; PowerShell uses the existing bridge IR. This change does not loosen
parsing, effect inference, target extraction, or path enforcement.

`ScriptFilePayload` is syntactic metadata with no filesystem access. Neither a
literal script path nor temporary script storage establishes its execution
effects. Unknown script effects still follow ordinary approval. Existing scripts
must not be rewritten just to change their structural classification.

## Model guidance

- Give each call one clear purpose. Related reads and aggregation may share a
  pipeline or script; do not split merely to shorten the command.
- Reuse collected data for totals and breakdowns. Split when a result must be
  inspected before the next action or authorization requires separation.
- Use literal argv for one executable and shell tools for shell source.
- For multi-step processing, write a readable script in the supplied temporary
  directory and invoke it by path. Existing scripts run as written.
- Finish when the requested result is supported and no concrete doubt remains.
  Code-size reports distinguish physical, nonblank and comment-excluded lines;
  regex approximations are not a language-aware line counter.

## Validation

`command_dispatch_test.go` exercises the actual assessment, approval and dispatch
path: first-call reads, saved write rules, always/never modes, unknown effects,
interactive approval/denial, unchanged arguments and temporary script payloads.
Existing assessment/conformance tests retain the effect and target invariants.

`TestRealAIBlackBoxCodeStatistics` checks tracked-file counts and physical lines
against an independent fixture, including spaces in paths, test files, directory
breakdowns and an untracked decoy. It runs with review enabled (`auto`) and with
`never` as a control. A four-call target is observational, never a runtime gate.
See [execution evaluations](2026-10-18-agent-execution-efficiency-evals.md).

## History

The earlier design called simplification advisory because exhaustion deferred to
approval, but its first two results were errors returned before dispatch. Those
still interrupted execution and induced rewrites. They are now removed rather
than relabeled. Historical `simplification_suggestions` metrics remain readable.

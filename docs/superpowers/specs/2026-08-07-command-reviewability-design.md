# Command reviewability design

Updated 2026-09-11: the structural constraint is advisory and pre-written script
payloads are exempt; see
[implementation plan](../plans/2026-09-10-cross-platform-command-reviewability.md).

## Goal

Make model-generated command calls small enough for a human to review without
removing the ability to execute legitimate shell pipelines, and without ever
forcing a rewrite of a command that is already correct.

Reviewability is independent of safety. A complex command can be read-only,
and a simple command can mutate external state. Existing access intent,
directory approval, dynamic-target, and secret approval rules still authorize
effects. The structural constraint is advice for the model, not an authorization
decision.

## Layers

1. Capability-aware system guidance prefers `process_run` for one executable
   and reserves shell tools for actual shell syntax.
2. The command's deterministic `CommandAssessment` reports structural
   reviewability facts without a second parse or an LLM call.
3. Every call may receive at most two simplification nudges per request. The
   nudges never block: the call proceeds to ordinary approval regardless.
4. Structured downloads carry explicit URL/path lists. Ordinary shell/process
   reviews paginate long content and escape terminal controls. Multi-step work
   belongs in a script file, which runs exactly as written.

## Assessment dimension

`CommandReviewability` reports a level (`simple`, `compound`, or `opaque`),
stable reason codes, an optional recommended tool, and whether the payload is a
pre-written script file. It is only one dimension of `CommandAssessment`.
Parser-derived action and pipeline counts live in `CommandShape`; runtime path
expressions live in `CommandAssessment.DynamicTargets`. Those fields drive the
nudges, while the review UI stays focused on the operation and affected target.

POSIX analysis uses the same mvdan AST as effect and path analysis. PowerShell
analysis uses the same bridge IR as effect and dynamic-target analysis. Parse
failure produces an opaque, unknown assessment; unknown effects fail closed.

Pipelines count as one purpose. Semicolon-separated or conditional branches
count as separate actions. Literal output decoration is advisory.

## Advice boundary

The preflight may advise on a single executable wrapped in shell, mixed
inspection and mutation, a dynamic write target, or multiple top-level actions,
using the separate `RequiresSimplification` decision. It never rewrites or
executes the source, and it never withholds execution.

The advice is local to one request and protected for parallel calls. Static reads
are skipped. Compound and opaque calls receive at most two nudges; changing the
tool name or payload does not reset that budget, and the next user request gets a
fresh one. Minimal mode keeps the nudges; explicit review-never bypasses them.

The advice never decides whether a command runs. When the budget is spent the
call goes to ordinary approval, where the user decides, so a command that is
already correct is never stopped, never lost, and never has to be rewritten. The
previous design rejected exhausted calls; that hard constraint was removed in
2026-09-11 because the correction pressure made the model restructure commands
that were already right — pre-written skill scripts in particular — and because a
dead end costs more than the review prompt it avoided.

Nudges describe structural facts but do not echo commands, dynamic target
expressions, secret references, or argument values, and they explicitly tell the
model to keep the operation's behavior identical and never to rewrite an existing
script file.

## Pre-written script payloads

Updated 2026-09-11. A command whose payload is a file rather than ad-hoc source
is exempt from the nudges, because the reviewer can inspect the file and asking
the model to restructure it is what breaks a skill's own scripts.

- `internal/approval/script_file.go` sets `Reviewability.ScriptFilePayload` for a
  whole command that is one interpreter or shell host with a literal script path
  (`bash scripts/build.sh`, `python tools/check.py`, `pwsh -File build.ps1`), or
  the path to a script itself (`./tools/release.sh`). `command_preflight.go`
  returns immediately for those calls.
- Ad-hoc source keeps the nudges: inline `-c`/`-e`/`-r`/`-m`/`-Command`,
  `-EncodedCommand`, `/c`, compound statements, pipelines, redirections, dynamic
  or glob operands, and dynamic command names.
- The test is syntactic with no filesystem access. It must behave identically for
  a script the app cannot resolve (a skill directory outside the workspace), and
  it must never become a path-authorization decision.
- Structural facts are unchanged by the exemption: the command is still reported
  as opaque where it is opaque, and it still reaches ordinary approval. Only the
  nudge is skipped.

## Non-goals

- Automatically split, rewrite, or reorder shell source.
- Infer semantic independence with an LLM.
- Treat complexity as mutation risk.
- Add persistent settings or database state.
- Infer all behavior of arbitrary native executables, imported scripts or remote services.

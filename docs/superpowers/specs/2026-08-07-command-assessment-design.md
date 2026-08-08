# Command assessment design

## Goal

Shell and direct-process approval uses one immutable fact bundle per tool
call. Static parsing, optional effect completion, access policy, directory
authorization, saved rules, correction guidance, and review presentation must
not independently reconstruct command state.

## Data model

`approval.CommandAssessment` contains four independent dimensions:

- `Effect` is `read`, `write`, or `unknown`. Unknown maps to write access so
  policy remains fail-closed.
- `KnownDirs` contains only concrete parser- or classifier-derived filesystem
  targets. A command's working directory is execution context, not by itself
  evidence that the command reads or writes that directory; unknown commands
  therefore keep `KnownDirs` empty unless analysis identifies a target.
- `DynamicTargets` contains runtime expressions and maps to
  `AccessIntent.UnresolvedPaths`; it never creates a `DirAllow` rule or
  authorizes a directory for tool execution. `DynamicProbe` is set only for a
  narrow allowlist of capability and path-resolution probes that do not read
  target contents. Those probes may proceed in `ReviewAuto` when no concrete
  directory is identified. Ordinary dynamic content reads, writes, unknown
  effects, and reads that also identify concrete directories require per-use
  approval.
- `Shape` and `Reviewability` describe composition and correction guidance.
  They do not decide whether execution is safe.

`CommandAssessment.AccessIntent()` is the only command-specific conversion to
the existing approval matrix. Database schemas, directory rules, review modes,
and public tool contracts are unchanged.

## Single-pass static analysis

POSIX source is parsed once with mvdan. Effect, concrete path arguments,
runtime expansions, top-level actions, pipelines, opaque syntax, and
reviewability are derived from that AST. The application does not run a
second POSIX parser during path extraction.

PowerShell source is sent to the persistent parser bridge once. Pure Go
functions derive every assessment dimension from the returned IR. The IR
records top-level value expressions and member expressions so standard
`$PROFILE` members and simple `$env:NAME` values can be proven as reads while
unknown properties, methods, assignments, control flow, redirection, static
members, encoded commands, and `Invoke-Expression` remain fail-closed.

Direct processes are assessed from literal `program`, `args`, and `cwd`.
Arguments are never interpreted as shell syntax, and an executable containing
a path is never trusted because its basename resembles an allowlisted command.
Bare program names are resolved once before approval and the resulting absolute
path is pinned to execution, so a later PATH lookup cannot select a different
program. Workspace and temporary-directory resolutions remain unknown-effect
and therefore reviewable. On Windows, `.bat` and `.cmd` launchers are rejected
because the operating system necessarily inserts shell parsing and cannot
preserve the literal argument-vector contract.

## Restricted LLM completion

The classifier runs only when static analysis leaves `EffectUnknown`. Its
default schema returns `effect`, concrete `affected_dirs`, and `reason`.
Legacy `needs_review` JSON and YES/NO custom prompts remain parse-compatible.
Contradictory or ambiguous output, timeouts, and invalid JSON leave the effect
unknown.

The completion cache stores only these LLM facts. For shell source, merge may
fill an unknown effect and add concrete directories. For `process_run`, LLM
completion contributes only effect and reason: arbitrary executable effects
cannot be safely bounded from program identity, and classifier-suggested
directories therefore never become authorization scope. Process directories
come only from deterministic argv analysis. Completion cannot replace
parser-derived shape, dynamic targets, opacity, or reviewability. Variables,
substitutions, placeholders, and non-literal directory markers are discarded.
User-visible fallback reason text is always `effects could not be proven`;
diagnostic details are debug-only.

## Approval flow

After argument validation, `toolCaller` creates the assessment exactly once
and passes it forward to:

1. the one-shot simplification preflight;
2. `AccessIntent()` and external-directory authorization;
3. saved-rule and review-mode policy evaluation; and
4. review summary and presentation rendering.

`requestApproval` consumes the completed assessment and intent. It never calls
a command analyzer. The UI derives risk tone from effect, dynamic labels from
`DynamicTargets`, and composition rows from `Shape`, preserving the separation
between security effect and human reviewability. When affected locations are
unknown, classifier-generated reason text remains available to debug logging
but is hidden from approval presentation and summaries because it cannot serve
as verified scope evidence.

## Invariants

- Unknown effects are write-like for policy but remain visibly unknown.
- Unknown locations never fall back to the workspace and never produce a
  reusable directory-approval rule.
- LLM output cannot erase static dynamic targets or AST structure.
- Dynamic capability probes with no concrete directory are auto-allowed in
  `ReviewAuto`; `ReviewAlways` retains its explicit always-review semantics.
- Dynamic content reads and all dynamic writes never match or produce reusable
  directory rules.
- At most one command-simplification correction occurs per user request; later
  compound calls enter normal approval.
- No persistent configuration or approval database migration is required.

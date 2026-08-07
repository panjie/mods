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
- `KnownDirs` contains concrete parser- or classifier-derived filesystem
  targets.
- `DynamicTargets` contains runtime expressions and maps to
  `AccessIntent.UnresolvedPaths`; it never creates a `DirAllow` rule or
  authorizes a directory for tool execution. The approval matrix treats a
  proven read with no concrete directory as capability discovery, so this
  intent does not block `ReviewAuto`. Writes, unknown effects, and reads that
  also identify concrete directories still require per-use approval.
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

## Restricted LLM completion

The classifier runs only when static analysis leaves `EffectUnknown`. Its
default schema returns `effect`, concrete `affected_dirs`, and `reason`.
Legacy `needs_review` JSON and YES/NO custom prompts remain parse-compatible.
Contradictory or ambiguous output, timeouts, and invalid JSON leave the effect
unknown.

The completion cache stores only these LLM facts. Merge may fill an unknown
effect and add concrete directories; it cannot replace parser-derived shape,
dynamic targets, opacity, or reviewability. Variables, substitutions,
placeholders, and non-literal directory markers are discarded. User-visible
fallback reason text is always `effects could not be proven`; diagnostic
details are debug-only.

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
between security effect and human reviewability.

## Invariants

- Unknown effects are write-like for policy but remain visibly unknown.
- LLM output cannot erase static dynamic targets or AST structure.
- Dynamic reads with no concrete directory are auto-allowed in `ReviewAuto`;
  `ReviewAlways` retains its explicit always-review semantics.
- Other dynamic reads and all dynamic writes never match or produce reusable
  directory rules.
- At most one command-simplification correction occurs per user request; later
  compound calls enter normal approval.
- No persistent configuration or approval database migration is required.

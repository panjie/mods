# Model-declared command effects

Accepted 2026-10-28. This replaces command analysis and command reorganization.

## Contract

The model supplies `effect: read_only | may_write` in every shell_run,
powershell_run and process_run call. The declaration describes the complete
invocation, including subprocesses and scripts. Mixed effects or uncertainty use
may_write. Missing, null, incorrectly typed or unknown values fail argument
validation before review or execution, including in review-mode never.

Every command also requires a nonblank string `purpose`: a brief explanation in
the user's language of what it does and why, including meaningful changes and
affected targets. This is model-provided reference text, never an authorization
signal or a verified assessment of safety.

In auto and always, read_only executes without review; may_write requires one-call
approval. No command uses saved directory/origin rules or temporary exemptions.
Without interactive review, may_write returns approval unavailable. Never retains
the existing user-controlled review bypass. Windows UAC remains independent.

Mods trusts the declaration. A mistaken read_only label may execute a mutation.
No classifier, parser, keyword filter, command allowlist or second model verifies
effects. Tests deliberately verify this contract with mock dispatch of misleading
declarations; those command samples are never executed.

Tool descriptions in normal and minimal mode require clear, reviewable commands,
prefer reads when they correctly complete the task, allow variables/loops/scripts,
and require an honest effect declaration. Necessary mutations are not to be
omitted or mislabeled to avoid approval.

## Implementation boundary

- tools/command_effect.go owns declaration values and validation. The shared
  command schema requires the field; registered command tools validate it.
- app/request_session.go prepares cwd, executable binding and a copy of the call
  arguments, then passes the declaration to review.go. There is no effect or
  target reanalysis before command dispatch.
- review.go skips target matching for commands. The UI asks "Run command?" and
  displays purpose, normalized working directory and the complete command, with
  approve/deny/cancel. Internal effect declarations and interpreter paths are
  omitted. Process arguments retain quoted boundaries and escaped newlines;
  multi-line shell commands retain line numbers. A single page omits pagination
  metadata; multiple pages show navigation. Long commands remain
  browsable without disabling approval. Commands offer neither simplify nor a
  saved permission action.
- Native filesystem and MCP capabilities, target rules, prepared access,
  revalidation and execution grants remain. Native writes cannot be relabeled by
  a model-supplied effect. Database tables and saved history remain readable.
- Execution timeouts, cancellation, process trees, secret handling, output limits,
  exit statuses, UAC and credentials remain in the existing execution layer.

Retired: internal/commandanalysis, its PowerShell bridge and catalog,
internal/commandtrace, app/command_analysis.go, command_organization.go,
command_reorganization.go and command_payload.go, together with their dedicated
tests. Shared remote-origin normalization moved into approval; native target
coverage types now belong to approval rather than aliasing analyzer types.

Removed config fields: command-simplification and
builtin-tools.shell-read-only-commands. Existing YAML ignores these keys; generated
config and help omit them. Existing user configuration and custom prompts are not
rewritten. Custom prompts that describe the retired analyzer should be updated by
their owner. Existing command-related saved grants do not exempt commands.

The shell parser dependency remains because Unix sudo execution still uses it;
it is not used to classify command effects.

## Verification and historical material

Use command_effect_test.go for approval/dispatch and schema/guidance tests. Native
authorization and execution tests remain. Repository check/test/build tasks are
the baseline; optional first-round model smoke tests are separate evidence, not
proof of classification accuracy or task completion.

The old app/testdata/command_analysis snapshots remain historical. The immutable
catalog and new model smoke evidence are retained in
../evidence/2026-10-28-command-effect/. Previously uncommitted Windows P5 test
sources were copied byte-for-byte to that experiment's retired-tests directory
before retiring their build dependency. Historical evidence is not re-scored.

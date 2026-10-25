# Bounded package-query analysis

The reported command `for p in dust duf; do pacman -Si $p 2>/dev/null | grep -E '^(Name|Version|Description)'; done; echo '--- installed?'; pacman -Q ncdu gdu dust duf btop 2>&1` parsed successfully but previously received unknown effects. Its two missing semantics were pacman option binding and loop-local variable binding.

## Implemented behavior

- `native/pacman` has a strict, independently tested query rule based on the [pacman manual](https://man.archlinux.org/man/pacman.8.en). Support includes a bounded subset of `-Q` queries and `-S` info/search/list/groups, short option bundles, exact long options, and `--`. Installation, removal, refresh, downloads, custom config/log/root options, unknown flags and abbreviations remain unknown. This is not a command-name trust rule.
- POSIX literal `for` loops can prove only built-in reads. Each literal binding is checked with the catalog; no shell is executed, no second parse occurs, and the original AST is not rewritten. Successful read targets are retained. Proofs are cached within one analysis and discarded before revalidation.
- Limits are 32 literal items per loop and 256 expanded statement visits across the assessment. Nested/dynamic loops, assignments, environment-binding commands, uppercase loop variables, unknown commands, persistent redirects and write commands remain unproven. The enclosing command must also satisfy the conservative context guard. Unquoted references cannot introduce word splitting or globbing from the bound values. These limitations intentionally favor ordinary approval over speculative interpretation.
- Quoted POSIX words decode escapes in their actual quote context. For example, `"^$p\$"` binds to `^gdu$`; a regex dollar sign is not a runtime shell expansion. `ExactRead` evidence distinguishes decoded argv and direct-process arguments from legacy projections, whose potentially dynamic arguments remain conservative. The pacman rule checks operations and options, not shell metacharacters in known literal operands.
- Diagnostics retain loop failure reasons (unsupported argument, unproven command, dynamic list or bound), as well as commands/arguments. The approval UI distinguishes unresolved expressions from established filesystem targets. Diagnostics never contribute authorization evidence and are deep-copied with assessment results.
- Shared script guidance prefers native multi-operand queries where equivalent. It does not require rewriting a command to pass review.

## Verification

`TestPacmanQueries`, `TestBoundedReadOnlyLoops`, `TestBoundedLoopRejectsUnprovenEffects`, `TestPackageQueryLoopApprovalDispatch`, and `TestLoopReviewExplainsUnmodeledSemantics` cover the reported command, strict query binding, rejected mutation/dynamic forms, expansion limits, and review labels. Approval dispatch uses a spy rather than invoking the system package manager. `TestPacmanQuotedQueryEvidence` and `TestQuotedLoopWordDecoding` cover anchored regex queries, literal process arguments, quoting and rejected dynamic expansions. `TestAnalyzeParsesPOSIXOnce` includes both reported commands. Original platform snapshots and the immutable legacy catalog corpus remain unchanged.

Linux build, root-module tests, and commandtrace baselines pass. This change does not add PowerShell loop support or claim Windows/macOS real-host validation.

## Mixed read/write coverage follow-up

Coverage now consults the existing recursive read-only proof before requiring concrete argv. Thus a known file write followed by `echo "$(wc -l < file)"` retains complete write coverage. The walk still visits substitution bodies and redirections: nested writes remain visible, and unknown operations remain incomplete. Parameter assignment and unproven arithmetic retain execution-context gaps. Arithmetic accepts numeric literals and elementary operators, with narrowly proven numeric output from single-count stdin `wc` and single-input `grep -c`; arbitrary read output is not assumed safe to reinterpret as arithmetic.

`cd` still produces a path-context gap rather than pretending that later relative paths use the original cwd. Diagnostics explain this and point to the existing `cwd` tool argument. The full reported counting command has complete coverage when the directory is supplied via `cwd`, and only a path-context gap with its leading `cd`.

Review rows deduplicate identical displayed targets and omit a resolved candidate already represented by a resolved read/write target. Separate read and write roles and unresolved candidates remain visible; underlying target facts, node identities and revalidation are unchanged. Incomplete coverage is described as an analysis limitation rather than an assertion that extra write targets exist.

`TestMixedReadSubstitutionsKeepWriteCoverage`, `TestMixedReadSubstitutionsPreserveUnprovenEffects`, `TestCodeCountDirectoryChangeExplained`, `TestMixedReadWriteApprovalDispatch`, and `TestCommandReviewDeduplicatesDisplayOnly` cover these changes. No specialized code-counting procedure was added to model prompts.

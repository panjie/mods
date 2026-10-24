# Catalog migration evidence

`catalog_legacy.json` contains 1,032 observations captured from commit `70b2737`
in an isolated worktree on Linux, using the pre-catalog readonly, write-target,
risk and bounded-write entry points. Every migrated identity has no-argument,
two-operand, unknown-option and `--` cases; subcommand families additionally
have a recognized and an unknown subcommand. PowerShell observations use the
host-independent invocation functions, not a running PowerShell parser.

The file is immutable migration evidence, not a specification of safe behavior.
`TestCatalogCompatibilityEntries` checks the new implementation against it and
requires coverage of every catalog identity. The explicit date/hostname read
exceptions are independently tested in `catalog_safety_test.go`; do not rewrite
the snapshot to absorb behavioral changes. `catalog_test.go` additionally tests
text output grammars, identity boundaries, trust, per-node evaluation and nested
wrapper budgets. App-level approval snapshots remain in
`internal/app/testdata/command_analysis` and are unchanged by this migration.

# Real Config Black-Box Test Design

## Goal

Validate the core `mods` CLI user journey through external commands only, using the current user's real `mods.yml`, real cache/database, and configured default AI provider. The tests should exercise behavior visible to a user rather than internal Go APIs.

## Scope

The first test pass covers the core real path:

1. Configuration discovery with `mods --dirs`.
2. A single real AI completion with the default provider and model.
3. JSON formatting through `--format --format-as json`.
4. Conversation persistence through `--title`, `--list`, and `--show`.
5. Context continuation through `--continue`.

Filesystem and shell tool execution are intentionally excluded from this first pass. Those behaviors depend on the model choosing to call tools, so failures can be ambiguous. They should be tested separately after the core CLI, provider, and persistence path is known to work.

## Execution Model

Build the local binary first, then run it as an installed-style executable from the command line. Each command records:

- Command name and purpose.
- Exit code.
- Standard output and standard error snippets.
- Any observable side effect, such as a saved conversation appearing in `--list`.

The run uses the real user configuration and real cache path. Test prompts use a unique marker based on the current date/time so saved conversations can be located without relying on unrelated history.

## Test Cases

### TC1: Configuration And Cache Discovery

Run `bin/mods.exe --dirs`.

Expected result:

- Exit code is `0`.
- Output includes both `Configuration:` and `Cache:`.
- The reported paths are non-empty.

### TC2: Default Provider Completion

Run a short deterministic prompt with `--raw --quiet --no-cache`.

Expected result:

- Exit code is `0`.
- Standard output is non-empty.
- Output contains the requested answer token or an equivalent direct answer.

### TC3: JSON Formatting

Run `--format --format-as json --raw --quiet --no-cache` with a prompt requiring a small JSON object.

Expected result:

- Exit code is `0`.
- Standard output contains parseable JSON after stripping optional Markdown fences.
- Parsed JSON contains the expected field/value.

### TC4: Conversation Persistence

Run a prompt with `--title <unique-title>` and no `--no-cache`, then run `--list` and `--show <unique-title>`.

Expected result:

- Completion exits `0` and returns non-empty output.
- `--list` exits `0` and includes the unique title.
- `--show <unique-title>` exits `0` and includes the prompt marker or assistant response.

### TC5: Conversation Continuation

Continue the saved conversation with `--continue <unique-title>` and ask for the previously supplied marker.

Expected result:

- Exit code is `0`.
- Output is non-empty.
- Output includes the marker or a clear reference showing previous context was loaded.

## Failure Handling

A failed command should be reported with its exit code and captured stderr. Provider/network/API failures are valid black-box failures unless the output clearly indicates an external outage or credential issue. JSON validation should tolerate Markdown code fences but should not accept malformed JSON.

## Verification Summary

The final report should list each test case as pass/fail, include the binary path used, identify the real configuration/cache paths discovered by TC1, and note any residual risk such as nondeterministic model output or provider-side rate limits.

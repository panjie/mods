# Complex Real Tool Black-Box Test Design

## Goal

Validate more complex `mods` CLI behavior through external commands only, using the user's real `mods.yml`, default AI provider, real cache/database, and real built-in tool execution. These tests exercise the agentic path where the model reads files, writes files, edits existing files, invokes shell tools, and continues a saved conversation.

## Scope

This second test pass covers real tool-enabled scenarios:

1. Build and run the local `bin/mods.exe` binary.
2. Read an existing file in a temporary workspace and answer with a unique marker.
3. Create a new file in the temporary workspace.
4. Edit an existing file while preserving unrelated content.
5. Use shell execution to inspect the temporary workspace.
6. Exercise plan mode and classify non-automatable approval prompts separately from failures.
7. Save and continue a tool-based conversation, then read a previously created file.

Web search and provider matrix testing remain out of scope for this pass. They introduce external nondeterminism that can obscure tool-chain regressions.

## Isolation

All file and shell side effects must stay under a run-specific directory in `C:\Users\panjie\AppData\Local\Temp\opencode`, named `mods-complex-blackbox-<timestamp>`. The repository working tree must not be used as the tool workspace. The real conversation cache may be written because the user explicitly allowed real configuration and data use.

## Execution Model

Use a temporary PowerShell harness outside the repository. The harness prepares workspace fixtures, invokes `bin\mods.exe` with CLI flags, captures exit code/stdout/stderr, verifies filesystem side effects, and writes a Markdown report.

Common CLI settings:

- `--workspace <temp-workspace>` scopes filesystem and shell tools.
- `--review never` disables review prompts for automated tool execution.
- `--raw --quiet` keeps output parseable.
- `--no-cache` is used except for the save/continue scenario.
- Prompt text contains explicit tool-use instructions and a unique marker.

## Test Cases

### TC1: Tool Read Existing File

Fixture: create `facts.txt` containing the unique marker.

Run `mods --workspace <temp> --review never --raw --quiet --no-cache "Read facts.txt and reply with only the marker."`

Expected result:

- Exit code is `0`.
- Output contains the marker.
- `facts.txt` remains unchanged.

### TC2: Tool Create New File

Run `mods --workspace <temp> --review never --raw --quiet --no-cache "Create generated.txt containing <marker> and reply done."`

Expected result:

- Exit code is `0`.
- `<temp>\generated.txt` exists.
- File content contains the marker.

### TC3: Tool Edit Existing File

Fixture: create `edit-target.txt` with a stable header, placeholder line, and stable footer.

Run `mods --workspace <temp> --review never --raw --quiet --no-cache "Replace PLACEHOLDER in edit-target.txt with <marker>; keep other lines unchanged."`

Expected result:

- Exit code is `0`.
- `edit-target.txt` contains the marker.
- Header and footer lines are still present.
- `PLACEHOLDER` is absent.

### TC4: Shell Tool Workspace Inspection

Run with shell enabled via config override or command-line-supported settings, asking the model to count files in the temporary workspace and write `shell-result.txt` with the count and marker.

Expected result:

- Exit code is `0`.
- `shell-result.txt` exists.
- File content contains the marker.
- File content includes a numeric count greater than or equal to the fixture files created before this case.

### TC5: Plan Mode Gate

Run `mods --plan --workspace <temp> --review never --raw --quiet --no-cache` with a prompt asking for a small file creation task.

Expected result:

- If the command exits `0` and creates the requested file, mark PASS.
- If the command exits or stalls because plan approval is required in a non-interactive harness, mark the case as PASS with classification `interactive gate observed` when stderr/stdout clearly indicates approval is required.
- Any provider/auth/tool setup error is a failure.

### TC6: Continue Tool-Based Conversation

Run a file-creation prompt with `--title <unique-title>` and no `--no-cache`, then continue the conversation with `--continue <unique-title>` asking the model to read the file and reply with the marker.

Expected result:

- Initial command exits `0` and creates the file.
- Continue command exits `0`.
- Continue output contains the marker.

## Failure Classification

Report each failure as one of:

- CLI/tool failure: non-zero exit or explicit tool error.
- Model compliance failure: command succeeds but expected file/output is missing.
- Interactive gate: plan approval cannot proceed non-interactively; only acceptable for TC5.
- Provider/environment failure: auth, rate limit, network, or service failure.

## Verification Summary

The final report must include the binary path, workspace path, unique marker, pass/fail/classification for each case, stdout/stderr snippets, and a final note confirming whether the repository working tree stayed untouched except for deliberate spec/plan files or bug fixes discovered during testing.

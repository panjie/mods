# fs_replace Windows Line-Ending Tolerance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `fs_replace` tolerate LF/CRLF differences between the file on disk and the model-supplied `old_text`, and preserve the file's line-ending convention when writing.

**Architecture:** Keep byte-exact matching as the primary path. When it finds nothing, normalize the file content and `old_text` to LF, re-count occurrences, and on a unique normalized match replace in normalized space and rewrite the whole result with the file's detected dominant line ending (same pattern as `applyCodexUpdate` in `codex_patch.go`). `new_text` always adopts the file's line-ending convention.

**Tech Stack:** Go, `internal/textutil.NormalizeLineEndings`, existing `detectLineEnding` helper.

**Spec:** Root-cause investigation in the 2026-09-14 session (no separate design doc). Failure: `fs_replace` does byte-exact `strings.Count` (`internal/tools/filesystem_write.go:101`) while `fs_read_file` line mode (`filesystem.go:354-368`, `bufio.ScanLines` drops `\r`) and `fs_search` (`filesystem.go:438`) present LF-only text; model tool-call JSON also emits LF. On CRLF files every multi-line/trailing-newline `old_text` therefore reports "was not found".

## Global Constraints

- Scope is `fs_replace` only; do not change `fs_write_file`, `fs_apply_patch`, or global file conventions.
- Preserve existing error semantics: 0 occurrences -> "was not found", >1 -> "matched N times"; uniqueness is enforced in normalized space too.
- LF-file behavior must not change (exact path stays byte-exact except `new_text` line-ending adaptation).
- All text files use Unix (LF) line endings.

---

### Task 1: Tolerant matching and line-ending-preserving write in fs_replace

**Files:**
- Modify: `internal/tools/filesystem_write.go` (`filesystemReplaceTool` Call body; add `replaceUniqueText`, `adaptLineEndings`; add `textutil` import)
- Test: `internal/tools/tools_test.go` (new tests after `TestFilesystemReplaceRequiresUniqueOldText`)

**Interfaces:**
- Consumes: `detectLineEnding(content string) string` (existing, `codex_patch.go`), `textutil.NormalizeLineEndings(s string) string`.
- Produces: `replaceUniqueText(content, oldText, newText string) (string, int)`; `adaptLineEndings(s, ending string) string`.

- [ ] **Step 1: Write the failing tests** (see session transcript for exact code; five tests: LF text in CRLF file, CRLF text in LF file, new_text adopts CRLF, normalized match still requires uniqueness, line-read round trip)
- [ ] **Step 2: Run `go test ./internal/tools -run TestFilesystemReplace -count=1` and confirm the new cases fail with "was not found"**
- [ ] **Step 3: Implement `replaceUniqueText` + `adaptLineEndings` and route the Call body through them**
- [ ] **Step 4: Add the tolerance sentence to the `fs_replace` description**
- [ ] **Step 5: Run `go test ./internal/tools -run TestFilesystemReplace -count=1` (all pass)**
- [ ] **Step 6: Run repo baseline `task check` and `task test`**

**Risk note:** The fallback path rewrites untouched mixed line endings to the file's dominant ending; this matches existing `fs_apply_patch` behavior. No config, prompt, or migration changes are needed.

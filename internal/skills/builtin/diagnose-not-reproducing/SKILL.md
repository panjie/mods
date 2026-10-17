---
name: diagnose-not-reproducing
description: Diagnose a change that does not take effect: stale cache, hot reload, copied install, or wrong component. Use when an edit, theme, plugin, setting, or build does not show up, or the user says 改动没体现 or 不生效.
---

# Diagnose a change that does not take effect

Use this procedure when the user reports that an edit, configuration change,
theme, plugin, setting, or build is not visible, or asks why a modification has
no effect. Do not start by reading implementation code.

1. Write the claim in one line: which screen, which action, what was expected,
   and what was observed. If the report does not identify them, ask before
   touching the system. Investigating an assumed target is the most common way
   to waste the whole turn.
2. Identify the artifact that is actually running: process start time, the
   loaded source directory or build id, file mtimes, version marker, and any
   official listing command (for example `omarchy-shell shell listPlugins`).
   The edited file is not evidence that the edited file is loaded.
3. Prefer the live system. Add the smallest readable instrumentation to the real
   component (a `console.log` or version bump), trigger its own reload or rescan
   path, read the process log, then revert the instrumentation. This is faster
   and more faithful than rebuilding a harness.
4. If you must isolate, copy the configuration verbatim. Never rewrite imports,
   versions, paths, or dependencies to make the copy start. List every
   difference from the real environment, and do not conclude anything the copy
   cannot support.
5. Treat a different failure signature as "not reproduced". If the real process
   reports one error and the copy reports another, the copy has not explained
   the real failure. Do not propose a fix from a signature that does not match.
6. Every probe needs a positive control: print the value you expect, not only
   the absence of a warning. An undefined color, path, or setting is often
   silent; "no warning" can mean the reference resolved to `undefined`.
7. Bound the investigation. After about ten inspections without a
   discriminating observation, stop: state the leading hypothesis, the single
   observation that would falsify it, and what you need from the user.
8. Report in three parts: Observed (what the running system actually shows),
   Inferred (your explanation), and Unverified (what remains unknown). Never
   present a sandbox result as confirmation of the user's environment, and do
   not propose edits to user source from an unvalidated copy.
9. Leave the system as you found it: revert instrumentation, stop temporary
   processes, remove temporary directories, and tell the user what changed.

## Common traps

- A compiled or on-disk QML/cache entry serving an older file.
- A second copy of the plugin, theme, or config being loaded instead of the edit.
- A package update or installer overwriting a git checkout, so the edit is gone.
- The change belongs to a component that this screen never instantiates.
- A setting is loaded from a different path than the one being edited.
- A warning about a property does not prove what the user sees; confirm the
  visual or behavioral symptom.

## Before proposing an edit

Name the observed failure signature you are fixing, cite the log line or probe
that shows it, and give the rollback command. If you cannot cite that evidence,
report the investigation as incomplete instead of proposing a change.

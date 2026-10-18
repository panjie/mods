# Terminal UI workflow improvements

Scope: rounds two and three of the terminal UI refresh. The theme and typography
from round one remain the shared foundation.

## Activity and plans

Completed tool calls keep a presentation-only record alongside their display
block. Consecutive successful native file reads collapse into one count; other
operations retain their own summary. Ctrl+O reveals arguments and output previews.
Failures automatically reveal a short preview. Preview text is redacted, stripped
of terminal styling, escaped for controls and bounded to 16 KiB per record and 200
rendered lines when expanded. The provider/session tool messages remain complete.
Raw stdout and non-interactive output retain their existing contracts.

Activity records are deep-copied with completion checkpoints so a model retry
restores the UI without repeating tool calls. Rendering uses the current terminal
width. Approval and input handlers receive keys before activity shortcuts.

The plan dock defaults to a small window around the current step; a completed
plan becomes a single progress line. Ctrl+P expands it, Up/Down browses earlier
or later steps, and Ctrl+P restores the compact view. Small terminals retain the
single-line fallback. No new configuration keys or session migrations are needed.

## Review

Affected targets precede commands and technical details. Download source/target
pairs remain adjacent. Reusable permission is labelled “Allow for session” and
identifies conversation lifetime, directory subtree coverage and exact remote
origins. Authorization, persistence and key semantics do not change.

Paged reviews reserve the measured height of wrapped headers and actions before
allocating content rows. Every page must still be shown before approval; resizing
resets page coverage. A terminal below the existing minimum cannot approve.

## Setup, history and help

Setup uses its actual conditional route to display step progress. Its action row
stays at a fixed compact height, with a smaller-screen fallback. Discovery state,
errors and recovery guidance appear beside the current choices; errors also have
a pinned short headline. Network work continues to deliver versioned messages and
does not mutate provider drafts.

The session browser uses the single-column layout from before the split-preview
change. Each session occupies one full-width row with its short ID, title, model,
provider and update time. The compact title bar and shortcut footer are restored;
there is no automatic preview loading. Enter opens the full transcript viewer,
including search, wrapping and copy. Filtering, continuation, marking and deletion
retain their existing controls.

CLI help starts with common examples and lists everyday flags before advanced
flags while preserving the complete public catalogue. Descriptions wrap by display
cells. Typed network and filesystem errors provide recovery guidance before their
technical detail. README documents the new controls.

## Validation

Regression coverage includes activity expansion/redaction and checkpoint isolation,
raw output preservation, plan navigation, review pagination at 40 and 80 columns,
session row truncation and continuation, setup progress
and stable action rows, Unicode wrapping, and help completeness.

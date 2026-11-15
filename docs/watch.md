# Watching command output

Watch mode observes one source and sends bounded output events into a saved
conversation. The model can investigate and use ordinary mods tools in response.
This first version supports an existing log file or a process launched by mods;
it does not attach to existing terminal windows, tmux panes, or GUI windows.

```sh
mods --watch file:./build.log --watch-match 'ERROR|FAIL' \
  'Investigate new build errors. Stop after diagnosing the first failure.'

mods --watch process --watch-match '^$' --watch-timeout 30m \
  'Run go test ./... as a monitored process. If it fails, fix the code and rerun it. Stop when it passes, or after three attempts.'
```

The file must already exist and be a regular file. Monitoring begins at EOF;
existing content does not trigger events. The user-selected file is opened once.
Removal, replacement/rotation, or an observed size decrease ends that source with
an explicit event. A replacement is never silently opened. Truncation followed
by regrowth between polls cannot always be detected; this is a polling source,
not an audit log.

In process mode, the model starts the command with `process_run`, `watch: true`,
and ordinary `effect`/`purpose` declarations. It returns an acceptance result;
startup failures, output, exit code and timeouts arrive as subsequent events.
The per-command timeout still applies (normally 60 seconds); ask for an
appropriate `timeout_ms` for a long job. Only one source may run at a time. A
replacement process may start after the previous completion event is consumed.
The process has no interactive stdin, elevation, secret_env or credential UI.
Programs may buffer output when stdout is a pipe. stdout and stderr are combined
for observation; their cross-stream order is not guaranteed.

## Triggers and limits

| Option | Default | Meaning |
| --- | --- | --- |
| `--watch` | disabled | `file:PATH` or `process` |
| `--watch-match` | empty | Go regular expression over new output; empty matches any output |
| `--watch-timeout` | `30m` | Total duration including setup, model work, approval waits and actions |
| `--watch-max-turns` | `10` | Setup plus event-driven conversation turns |
| `--watch-max-requests` | `100` | Provider rounds including tool follow-ups and summaries |

Process completion and source failure always trigger, even without a regex
match. `--watch-match '^$'` suppresses nonempty-output matches for exit-driven
process workflows. Regular expressions examine bounded overlapping windows of
up to 64 KiB within a batch, not an unlimited log; there is no guarantee for
patterns requiring larger context. ANSI escapes are not removed before matching.

There is a two-second minimum interval between output batches; completion
bypasses it. Collection continues during model work and approvals. At most
32 KiB of pending output is retained; older bytes are dropped and their count
is included in the event. A remembered match still triggers if its text has
been dropped. The model must treat missing context as incomplete coverage.

Only one model/action turn runs at a time, after the preceding turn and tool
result groups finish. Provider-round budgets also count follow-up requests
within a turn; transport retries within one round use the existing
`--max-retries` setting. Limits bound work, not an exact token count or cost.
A natural-language retry limit is followed by the model; the invocation time,
turn and request limits are enforced independently.

## Controls and permissions

While waiting interactively, the status panel shows the source, last output time,
batch count, buffered bytes and dropped bytes. Space pauses/resumes event
handling. Pausing continues bounded collection, so output may be dropped.
Ctrl+C, Esc or q stops the waiting invocation. During model/action execution,
the existing cancellation and approval controls apply. Raw/noninteractive mode
has status lines and signal cancellation, without the keyboard pause panel.

The model can call `watch_control` with `status` or `stop`. Stopping, reaching a
limit, cancellation or an error cleans up the owned process tree. Stopping a
file watcher does not terminate the program writing that file.

Monitoring never authorizes later writes. `process_run watch=true` goes through
the existing command declaration/review gate, and its review identifies the
background lifetime. All action tools retain their usual review and path
checks. Y/N/F/cancel keep their existing meanings. F applies to later watch
turns in this invocation only. Noninteractive runs cannot answer a write review;
use an interactive terminal when approvals may be needed.

Observed output is untrusted data, not permission or instructions. Selected
output and action results are sent to the configured provider and saved in the
conversation. `--watch` requires saving, cannot combine with `--chat` or one-shot
actions, and has no persisted monitor state. Continuing a saved conversation
does not restart monitoring; a new explicit `--watch` invocation is required.

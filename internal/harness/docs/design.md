# How It Works

The package is small on purpose. Each file does one job.

| File               | Job                                                          |
| ------------------ | ------------------------------------------------------------ |
| `preset.go`        | The built-in harness table: command and model flag          |
| `detect.go`        | Find binaries on `PATH` and common install folders           |
| `resolve.go`       | Turn a preset name or template into a runnable `Command`     |
| `models.go`        | Ask a harness for its live model list                        |
| `run.go`           | Run a command with timeout, streaming and output tails       |
| `proc_unix.go`, `proc_windows.go` | Kill the whole process group on cancel        |

## Flow

```
Detect ──> Resolve(name, model) ──> Command ──> Run(prompt) ──> Result
 (which        (verify binary,        (argv with        (execute, capture
  exist?)       insert model flag)     {prompt})         output)
```

## Decisions

**Prompts are never passed through a shell.** `Run` replaces `{prompt}` inside
an argv element and executes the binary directly, so no quoting is needed and
a prompt cannot inject shell syntax.

**Presets are headless and auto-approving.** Every harness would otherwise
start an interactive session and wait for approval. Each preset carries the
flag that disables that and the flag that lets it edit without asking.

**stdin is empty.** A harness that still tries to ask something fails
immediately rather than hanging until the timeout.

**The process group is killed on cancel.** Harnesses start helper processes.
Killing only the parent would leave them running. On Windows, Go's default
kill is used.

**Models come from the harness, never from a list in this package.** Vendors
release and retire models constantly, so any hardcoded list is stale. Listing
means running the harness, which can be slow, so it is separate from `Detect`,
bounded by a 5s timeout, and cached.

**A harness is only supported if it can list its models.** Without that, the
caller would have to guess model names. This is why gemini, codex, aider and
goose are not included: none of them has a non-interactive list command.

## Add a Harness

1. Add a `Preset` to `presets` in `preset.go`. Include the flags that make it
   non-interactive and auto-approving, and put `Placeholder` where the prompt
   goes. If the prompt follows its own flag (like `-p`), the model flag is
   inserted before that flag automatically.
2. Add the command that lists its models, and a parser for the output, to
   `listers` in `models.go`. A preset without one is rejected by a test. Test
   the parser with a sample of the real output. If the harness cannot list its
   models, it is not a fit for this package.
3. Add a row to [harnesses.md](harnesses.md). A test fails if a preset is missing there.
4. Run `go test ./...`.

## Not Included

This package only detects and runs harnesses. It runs one at a time, and has no
job queue, settings storage, UI, or tracking of which files a harness changed.
Callers own those.

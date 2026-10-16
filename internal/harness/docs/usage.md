# Usage

## Find Installed Harnesses

```go
for _, h := range harness.Detect(nil) {
	fmt.Println(h.Name, h.Model, h.Installed, h.Path)
}
```

Each `Harness` returned by `Detect` has:

| Field       | Meaning                                                      |
| ----------- | ------------------------------------------------------------ |
| `Name`      | the harness, e.g. `claude`. Pass to `Resolve`                |
| `Model`     | the model you chose for it; empty means the harness uses its own. Pass to `Resolve` |
| `Installed` | whether the binary was found                                 |
| `Path`      | where the binary was found                                   |
| `Cmd`       | the command line that would run, with `{prompt}` unfilled    |

`Detect` checks every built-in harness on each call. It never runs one.
Pass a map of harness name to model (for example `{"claude": "sonnet"}`) to
have `Model` and `Cmd` reflect your choice; `nil` chooses none.

`harness.Installed()` returns just the names that were found.

A harness is found on `PATH` first, then in folders installers commonly use
that are often missing from `PATH` (`~/.local/bin`, `~/.cargo/bin`,
`~/go/bin`, the npm global bin, and a few others). See `BinDirs`.

## Choose a Model

There is no built-in default model. To see which models the installed
harness offers, ask it:

```go
models, err := harness.DiscoverModels(ctx, h.Name)
```

This runs the harness's own list command and returns what it prints, in order.
There is no built-in fallback list. It returns an error if the harness is not
installed or its list command fails. Successful results are cached for the
process.

## Resolve a Harness

`Resolve` takes the harness name and the model, which are the `Name` and
`Model` fields from `Detect`:

```go
cmd, err := harness.Resolve(h.Name, h.Model)
cmd, err := h.Resolve() // same thing
cmd, err := harness.Resolve("claude", "sonnet") // or write them directly
cmd, err := harness.Resolve("claude", "")       // harness picks its own model
```

It accepts a harness name (case-insensitive) or a command template. It fails
if the binary is not installed.

An empty model means no model flag is passed, so the harness uses its own
configured model. A template that contains `{model}` needs a non-empty model.

A template is a space-separated command. It must contain `{prompt}` and may
contain `{model}`:

```go
cmd, err := harness.Resolve("mytool --model {model} --task {prompt}", "fast")
```

Templates are split on whitespace, so a single argument cannot contain a space.

## Run

```go
res := harness.Run(ctx, cmd, "rename foo to bar in main.go", harness.Options{
	Dir:     "/path/to/repo",
	Timeout: 5 * time.Minute,
	Stdout:  os.Stdout, // optional live output
})
```

| Field        | Meaning                                                        |
| ------------ | -------------------------------------------------------------- |
| `res.Err`    | nil on success, `ErrCancelled`, a timeout error, or exec error |
| `res.Stdout` | tail of stdout (last 32 KiB by default)                        |
| `res.Stderr` | tail of stderr                                                 |
| `res.Duration`| how long the run took                                         |

Cancel by cancelling the context. The harness and any processes it started
are killed.

Presets run non-interactively and apply edits without asking. Only run them
on code you are willing to let an agent modify.

## Options

| Option     | Default      | Purpose                                      |
| ---------- | ------------ | -------------------------------------------- |
| `Dir`      | current dir  | working directory for the harness            |
| `Timeout`  | 10 minutes   | maximum run time                             |
| `LogBytes` | 32 KiB       | output kept per stream in the `Result`       |
| `Stdout`   | none         | live copy of stdout                          |
| `Stderr`   | none         | live copy of stderr                          |
| `Env`      | none         | extra `KEY=VALUE` environment entries        |

# Harness

Detect coding-agent CLIs (claude, cursor-agent, agy, opencode, codex) installed on this
machine and run them headless.

```sh
go get github.com/px0-ai/harness
```

```go
// 1. Detect: every known harness and whether it is installed.
for _, h := range harness.Detect(nil) {
	fmt.Println(h.Name, h.Installed) // e.g. claude true
}

// 2. Resolve: pass a harness name and a model. "" lets the harness use its own.
cmd, err := harness.Resolve("claude", "sonnet")

// 3. Run.
res := harness.Run(ctx, cmd, "rename foo to bar", harness.Options{Dir: repo})
fmt.Println(res.Stdout, res.Err)
```

Built-in: claude, cursor-agent, agy, opencode, codex
([details](docs/harnesses.md)).
Any other CLI works with a template like `mytool --task {prompt}`.

Presets run non-interactively and auto-approve edits.

- [Supported Harnesses](docs/harnesses.md)
- [Usage](docs/usage.md)
- [How It Works](docs/design.md)

## License

[MIT](LICENSE)

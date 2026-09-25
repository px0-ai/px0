<picture>
  <source media="(prefers-color-scheme: dark)" srcset="https://px0.ai/logo/px0-logo-dark.png">
  <source media="(prefers-color-scheme: light)" srcset="https://px0.ai/logo/px0-logo-light.png">
  <img alt="px0" src="https://px0.ai/logo/px0-logo-dark.png" width="120">
</picture>

---

px0 is the IDE for humans and AI, optimized for quick, fast code reviews. It turns your browser into a zero-latency console with native Git and GitHub integrations, instant search across massive codebases, and seamless handoff to local AI coding harnesses.

As AI agents author more code directly from the terminal, engineering productivity is no longer constrained by how fast you type—it is constrained by how fast you can review, navigate, and verify changes. px0 replaces heavy, multi-gigabyte editing suites with an instant, distraction-free environment built specifically for this review loop.

- < 1 ms cold start
- ~20–30 MB server daemon RAM (~100–180 MB total with browser tab, vs. VS Code's ~1,440 MB)
- Single static Go binary with zero runtime dependencies (no Electron, no Node, no CGO)
- ~280 languages tokenized natively via Chroma

See full performance benchmarks and comparisons at [px0.ai/#bench](https://px0.ai/#bench).

## Features

- GitHub PR reviews & Git panel: Review pull requests directly (`px0 <pr-url>`), inspect scoped merge-base diffs, draft inline review comments, and stage, commit, or push from the browser.
- AI coding harness integration: Dispatch edits directly to Claude Code, Gemini CLI, Cursor Agent, Antigravity, OpenCode, Codex, Aider, or Goose with live reloading.
- Fast navigation: Fuzzy file search, symbol outline, and workspace regex search in milliseconds.
- Remote-first: Run on any remote server, VM, or container and browse locally without SSH key setups or remote daemons.
- Virtual rendering: Opens 400,000-line files smoothly by mounting only visible rows; frees memory back to the OS after 15 seconds of inactivity.
- Rich code viewer: 14 built-in themes, rendered Markdown preview, image inspector, and optional zero-config LSP for Go-to-Definition and hover.

## Installation

Quick install (macOS, Linux, BSD):

```bash
curl -fsSL https://px0.ai/install.sh | sh
```

Build from source (requires Go 1.24+ and Node.js or Bun):

```bash
git clone https://github.com/px0-ai/px0.git
cd px0
make build
install -d ~/.local/bin && install px0 ~/.local/bin/
```

To update an existing installation:

```bash
px0 --update
```

## Usage

```bash
# Inspect current directory
px0

# Inspect a specific directory or file
px0 ~/workspace/project
px0 main.go:42

# Review a GitHub pull request
px0 https://github.com/owner/repo/pull/123

# Remote or headless server mode
px0 -host 0.0.0.0 -port 7777 ~/workspace

# Behind a reverse proxy under a subpath
px0 -base-path /rev-123/ -host 0.0.0.0 -port 7777 ~/workspace
```

## Development

```bash
git clone https://github.com/px0-ai/px0.git
cd px0

# Run tests
make test

# Live frontend development (serves web/ assets directly from disk)
go run . -dev .

# Build binary
make build
```

## Benchmarks

```bash
# Clone the benchmark corpus (~3 GB: Linux, Kubernetes, TypeScript, etc.)
./benchmark.sh --clone

# Run benchmarks across the corpus
./benchmark.sh

# Compare px0 directly against VS Code on the current workspace
./benchmark.sh --vscode .

# Profile memory lifecycle (index, search, idle memory release)
./benchmark.sh --memory bench-repos/linux
```

> **Architecture & Memory Accounting**: px0 separates the lightweight native Go daemon from the browser-rendered UI. The Go server consumes ~20–30 MB resident RAM (RSS). The active browser tab allocates ~80–150 MB for DOM nodes, V8 JS runtime, and GPU compositing. Combined, the total local memory footprint is ~100–180 MB (~85–90% lighter than Electron IDEs like VS Code at ~1,440 MB, which bundle dedicated Chromium and Node runtimes). In remote or container environments (`px0 -host 0.0.0.0`), the remote host pays strictly the ~20–30 MB server cost. See [BENCHMARKS.md](BENCHMARKS.md) for full methodology and breakdowns.

## Documentation

- [Docs](https://px0.ai/docs)
- [Benchmarks](https://px0.ai/#bench)
- [Blog](https://px0.ai/blog)
- [Changelog](https://px0.ai/changelog)
- [Partners](https://px0.ai/design-partners)

## Design Partners

When AI coding agents generate large diffs across repositories daily, code authoring is no longer the bottleneck—verification and review is.

We are looking for engineering teams (10+ engineers or teams running active agent workflows) as design partners. Partners receive direct Slack Connect access to the core team, rapid turnaround on custom harness integrations, and direct input on the roadmap.

Learn more on the [Design Partners page](https://px0.ai/design-partners).

## License

[MIT License](LICENSE) © Arpit Bhayani

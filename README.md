# px0: Read Code. Fast.

px0 is a fast, lightweight, read-only IDE designed for instant code navigation and review in your browser. Booting in under 1 ms and using ~16 MB of RAM, it turns your browser into a zero-latency inspection console with symbol-level navigation, deep search, and syntax highlighting across massive codebases.

### Why a Read-Only IDE?

More and more, code generation happens directly in the terminal—driven by coding agents, CLI tools, and background orchestrators. We spend less time hand-typing boilerplate inside heavy, sluggish editors that consume gigabytes of memory and take seconds to boot.

Instead, the developer's role is shifting toward review, navigation, and audit:

• Terminals drive generation: Tools generate the code, execute tests, and manage workflows.
• Developers drive verification: We need to quickly inspect diffs, verify symbol references, trace definitions, and sanity-check architecture.
• Desktop IDEs are overkill for review: Launching a massive Electron app or heavy IDE suite just to inspect generated code wastes time and system resources.

When you're reviewing code, you don't need a heavy editing environment—you need an instant, zero-latency window into your codebase. px0 is built for this: sub-millisecond startup, deep code intelligence, and instant search with virtually zero footprint.

---

## Installation

### Option 1: Quick Install (macOS, Linux, BSD)

Install or upgrade to the latest release with a single command:

```bash
curl -fsSL https://raw.githubusercontent.com/px0-ai/px0/master/install.sh | bash
```

### Option 2: Prebuilt Binaries

Download the binary for your operating system and architecture directly from GitHub Releases or `dist/`, make it executable, and move it to your `PATH`:

```bash
# macOS (Apple Silicon)
sudo install dist/px0-0.1.0-darwin-arm64 /usr/local/bin/px0

# Linux (x86_64)
sudo install dist/px0-0.1.0-linux-amd64 /usr/local/bin/px0
```

### Option 3: Build from Source

Requires Go 1.24 or newer. No npm, no node, no CGO, and no system libraries required:

```bash
git clone https://github.com/px0-ai/px0.git
cd px0
make build
sudo install px0 /usr/local/bin/
```

To cross-compile binaries for all 15 supported OS and architecture combinations:

```bash
make dist
# or ./build.sh
```

---

## Features

- **Blazing Fast Code Navigation**: Fuzzy search files (`Cmd/Ctrl+P`), document symbols (`Cmd/Ctrl+Shift+O`), and full project regex scan (`Cmd/Ctrl+Shift+F`) in milliseconds.
- **Rich Syntax Highlighting**: Built-in native tokenization for ~280 languages via Chroma.
- **Rendered Markdown & Mermaid Diagrams**: `.md` files open as a rendered GitHub-Flavored Markdown document (`Alt+M` toggles source/preview), with theme-highlighted code fences and Mermaid diagrams in zoomable cards.
- **Custom Themes**: Ships 14 built-in themes, including Tokyo Night (default), Paper, Catppuccin, Dracula, GitHub Dark, Gruvbox, Monokai, Nord, One Dark, Rose Pine, and Solarized. Switch via the button at the bottom of the sidebar or `Select Theme` in the command palette. See [STYLING.md](STYLING.md) to write your own.
- **Optional Language Server Protocol (LSP)**: Zero-config auto-detection of local LSPs (`gopls`, `rust-analyzer`, `pyright`, `typescript-language-server`, `clangd`, etc.) for precise Go-to-Definition (`F12`), Hover info, and cross-references. Falls back automatically to instant regex outlines when no LSP is installed.
- **Virtual DOM / Zero Overhead**: Opening a 400,000-line file costs the same as a 10-line file; only visible lines render in the browser.
- **Clean Terminal Experience**: CLI adheres to the Ape design spec with a subtle 256-color palette, Unix pipe detection, and quiet automation modes.
- **Completely Self-Contained**: Single static binary embedding HTML, CSS, and JS. Zero runtime dependencies, no electron, and no cloud phone-homes.

---

## Language Server (LSP) Setup (Optional)

`px0` works entirely out of the box without any language servers—fuzzy file search, project grep, and outline parsing are completely built-in.

However, having language servers installed gives `px0` superpowers: semantic Go-to-Definition (`F12`), type hover docs, and jump-to-definition into standard library files. px0 does not ship any language server. It detects the ones below if they are on your `PATH` or in the usual install folders (`~/go/bin`, `~/.cargo/bin`, `~/.local/bin`, npm's global folder, and Homebrew's folders on macOS). Where a language has several, the first one found in the order listed is used:

| Language | Server | Quick Install Command |
| -------- | ------ | --------------------- |
| **Go** | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| **Rust** | `rust-analyzer` | `rustup component add rust-analyzer` |
| **TypeScript / JavaScript** | `typescript-language-server` | `npm install -g typescript-language-server typescript` |
| **Python** | `pyright`, `pylsp` or `ruff` | `npm install -g pyright` or `pipx install python-lsp-server`. `ruff` (`pip install ruff`) is detected too, but gives no call trails |
| **C / C++** | `clangd` | `sudo apt install clangd` or `brew install llvm` |
| **Zig** | `zls` | `brew install zls` or download from [zigtools/zls](https://github.com/zigtools/zls) |
| **Lua** | `lua-language-server` | `brew install lua-language-server` (macOS) |
| **Ruby** | `solargraph` | `gem install solargraph` |
| **Java** | `jdtls` | `brew install jdtls` (macOS) |
| **C#** | `omnisharp` | Install OmniSharp and put `omnisharp` on `PATH` |
| **LaTeX** | `texlab` | `brew install texlab` (macOS) |

Servers are spawned **lazily on first request** for that file type and shut down cleanly upon exit. You can also disable LSP detection entirely at any time using `px0 -no-lsp`.

No server for the file you are reading? The status bar shows **LSP: set up**. Click it, open the Calls tab, or run `Set Up Language Server…` from the command palette to see the install options for your OS. px0 runs user-level installers (`go`, `rustup`, `npm`, `pipx`, `gem`, `brew`) for you on request, finds the result in the usual install folders even when they are not on `PATH`, and starts the server without a restart. Installers that need an administrator (`sudo apt`, `winget`) are shown for you to copy and run. px0 has no installer for `zls`, `lua-language-server`, `jdtls` or `texlab` outside macOS, or for `omnisharp` and `ruff` anywhere: install those yourself, then click **Detect and start**. Installing only works from px0's own page opened by IP address or `localhost`.

---

## Why a Dedicated Code Viewer?

Traditional IDEs (like VS Code and JetBrains) were architected when developers spent almost all their time manually typing code. They carry tens of thousands of editing features, bloated Electron/Node runtimes, complex file watchers, heavy background extensions, and gigabytes of memory overhead.

In the modern development workflow—with AI coding agents, fast branch reviews, pull requests, and automated generation—developers spend significantly more time **inspecting, reviewing, navigating, and understanding codebases** than typing boilerplate.

| Parameter | Traditional IDE (such as VS Code) | px0 (Code Viewer) |
| --------- | --------------------------------- | ----------------- |
| Primary Purpose | Manual code authoring and plugin host | Instant code reading and navigation |
| Base Memory (RSS) | ~1,440 MB (1.4+ GB) | ~16 MB (80x - 90x lighter) |
| Active Startup CPU Spike | 35% - 50% | < 1% |
| Cold Startup Time | Several seconds | Sub-millisecond |
| Process Tree | 15+ Node.js/Electron processes | 1 single static Go binary |
| Workspace Indexing | Multi-second background churn | 0 - 45 ms for entire repositories |
| Setup and Config | Config files, plugins, node, npm | Zero config, zero runtime |

---

## Key Numbers and Benchmarks

All metrics are measured on real-world repositories and reproducible using [`./benchmark.sh`](benchmark.sh).

### Real Corpus Performance (px0 standalone)

| Repository | Source Size | Files Indexed | Index Time | Fuzzy Search | Full-Tree Regex Scan | Resident RAM (RSS) |
| ---------- | ----------- | ------------- | ---------- | ------------ | -------------------- | ------------------ |
| flask | 3 MB | 235 | 1 ms | 0.8 ms | 2.3 ms | 16 MB |
| redis | 26 MB | 1,855 | 13 ms | 1.0 ms | 18.2 ms | 17 MB |
| react | 63 MB | 7,178 | 52 ms | 2.7 ms | 32.2 ms | 21 MB |
| django | 74 MB | 7,014 | 39 ms | 1.3 ms | 26.8 ms | 20 MB |
| kubernetes | 370 MB | 25,926 | 150 ms | 13.5 ms | 84.6 ms | 30 MB |
| TypeScript | 414 MB | 66,533 | 566 ms | 6.2 ms | 150.3 ms | 69 MB |
| linux kernel | 1,809 MB | 95,710 | 370 ms | 6.0 ms | 451.8 ms | 55 MB |

### Head-to-Head: px0 vs. VS Code

Run `./benchmark.sh --vscode .` to measure both on your active machine:

```
### px0 vs. VS Code Comparison

| Metric / Parameter | px0 | VS Code (Server/Remote) | Notes |
| ------------------ | --- | ----------------------- | ----- |
| **Memory (RSS)**   | **15 MB** | **1,166 - 1,440 MB**    | ~80x lighter |
| **Instant CPU %**  | **0.0%**  | **4.0% - 39.0%**        | Minimal CPU churn |
| **Index Time**     | **< 1 ms**| **~4 - 10 s**           | px0 is instantaneous |
| **Process Count**  | **1 single Go binary** | **15+ processes** | Multi-process Node tree |
```

---

## Usage

Run `px0` pointing to any directory:

```bash
px0                 # view the current workspace
px0 ~/src/kernel    # view another repository
```

`px0` starts the local viewer, prints the URL, and opens your default browser immediately.

### Updating px0

To check for updates and automatically upgrade `px0` to the latest release:

```bash
px0 --update
```

`px0` also checks asynchronously in the background once every 24 hours without delaying startup (<1 ms) and notifies you on stderr when an update is available.

### CLI Flags

| Flag | Default | Description |
| ---- | ------- | ----------- |
| `-port N` | `7777` | Port to listen on (`0` picks an ephemeral free port) |
| `-host H` | `127.0.0.1` | Local address to bind |
| `-no-open` | `false` | Do not launch the web browser automatically |
| `-no-lsp` | `false` | Disable language server discovery and use regex-based outline |
| `-no-color` | `false` | Strip ANSI escape sequences from terminal output |
| `-quiet` | `false` | Suppress CLI narration (errors still print to stderr) |
| `-update` | `false` | Check for updates and install the latest version |
| `-version` | `false` | Print version and architecture and exit |

---

## Keyboard Shortcuts

`Cmd` on macOS, `Ctrl` on Windows and Linux; `Alt` is `Option` on a Mac. The in-app sheet (`?`), footer hints and tooltips show each key the way your keyboard labels it (`⌘⇧F` on a Mac, `Ctrl+Shift+F` elsewhere).

| Key | Action |
| --- | ------ |
| `Cmd/Ctrl+K` | Universal palette / quick open |
| `Cmd/Ctrl+P` | Go to file |
| `Cmd/Ctrl+Shift+P` | Command palette |
| `Cmd/Ctrl+Shift+O` | Go to symbol in file |
| `Cmd/Ctrl+Shift+F` | Full workspace search |
| `Cmd/Ctrl+F` | Find in active file (seeded with the current editor selection) |
| `Cmd/Ctrl+G` | Jump to line |
| `F12`, `Cmd/Ctrl+Click` | Go to definition |
| `Shift+F12` | Find all references |
| `←` / `→`, `Home` / `End` (`Cmd+←` / `Cmd+→` on macOS) | Move the (read-only) caret along the line; click places it |
| `Ctrl+Home` / `Ctrl+End` (`Cmd+↑` / `Cmd+↓` on macOS) | Top / bottom of file |
| `Alt+Z` / `Alt+L` / `Alt+M` | Toggle word wrap / line numbers / rendered Markdown preview |
| `Alt+C` / `Alt+A` / `Alt+U` | With code selected: copy reference / copy for agent / find usages |
| `Alt+Shift+H` | Call trail: callers and callees of the function under the cursor, expandable level by level |
| `Hover` | Type signature & doc hover |
| `Cmd/Ctrl + Hover` | Inspect identifier link |
| `Alt+Left` / `Alt+Right` | Navigate back / forward in history |
| `Cmd/Ctrl+B` | Toggle file tree sidebar |
| `Alt+W` | Close active tab (`Cmd/Ctrl+W` too, where the browser lets a page have it) |
| `Ctrl+Tab` | Switch to next tab |
| `Alt+1` … `Alt+9` | Select tab by position |
| `?` | Show all keyboard shortcuts |

---

## Philosophy and Design Principles

- **Read-Only by Design**: px0 does not attempt to be a code editor. Code authoring belongs to AI agents, CLI tools, or dedicated editors. px0 focuses exclusively on the reader experience.
- **Snapshot Indexing**: By omitting heavy filesystem watcher daemons (`inotify` leaks, perpetual background CPU spikes), indexing completes in milliseconds. Re-index whenever needed via `Cmd+Shift+P` -> `Re-index Workspace`.
- **Local and Private**: Runs locally on `127.0.0.1` with zero telemetry, zero accounts, and zero cloud phone-homes.
- **Ape Terminal & Web Aesthetics**: Minimal, quiet, high information density, designed for pair-programming and flow state.

---

## Reproducing Benchmarks

All benchmark figures can be measured directly on your own system:

```bash
# 1. Fetch benchmark corpus (~3 GB shallow clones of Linux, K8s, TypeScript, etc.)
./benchmark.sh --clone

# 2. Run the full benchmark suite
./benchmark.sh

# 3. Compare px0 directly against VS Code process tree on your workspace
./benchmark.sh --vscode .

# 4. Profile memory lifecycle across index, search, and idle recovery
./benchmark.sh --memory bench-repos/linux

# 5. Measure LSP latency (definition, hover, references)
./benchmark.sh --lsp .
```

See [BENCHMARKS.md](BENCHMARKS.md) for full methodology and detailed charts.

---

## Contributing

Contributions that keep px0 fast, minimal, and dependable are welcome. Please read [CONTRIBUTING.md](CONTRIBUTING.md) before submitting issues or pull requests.

### Development Workflow

1. Clone the repository:

```bash
git clone https://github.com/px0-ai/px0.git
cd px0
```

2. Run tests:

```bash
make test
# or go test ./...
```

3. Live frontend development (serves `web/` assets from disk without rebuilding the binary):

```bash
go run . -dev . .
```

4. Verify CLI formatting and builds:

```bash
go vet ./...
make dist
```

### Architecture Overview

- `main.go` / `ui.go`: CLI entrypoint, flag parsing, signal management, Ape terminal experience.
- `update.go`: Self-updater and asynchronous daily version check.
- `server.go`: HTTP routes, JSON API, gzip compression, and embedded asset serving.
- `index.go`: Concurrently walks workspace, honors `.gitignore` (ignored files stay visible but dimmed in the explorer, and are never indexed or searched), builds in-memory path and trie structures in milliseconds.
- `search.go` / `fuzzy.go`: High-performance substring and fuzzy file/symbol matching algorithms.
- `lsp.go` / `lspnav.go` / `calls.go`: Lightweight JSON-RPC client communicating with local language servers over stdio, plus definitions, references and call trails.
- `lspservers.go` / `lspsetup.go`: Language server registry, discovery, and install on request.
- `web/`: Native zero-dependency ES module frontend (custom virtual scroll, syntax highlight rendering, tab manager).
- `web/themes/`: One CSS file per colour theme, joined by the server into `/static/themes.css`. Token reference in [STYLING.md](STYLING.md).

---

## License

[MIT License](LICENSE) (c) 2026 Arpit Bhayani

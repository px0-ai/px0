# Performance Benchmarks & Methodology

## 1. Requirements

- Go 1.24+: To build the target binary.
- Git: Required only for `--clone`.
- System Utilities: `curl`, `awk`, `find`, `du` (standard on Linux and macOS).
- Disk Space: ~3 GB for the standard multi-repository corpus.
- Memory Measurements: Read via `/proc`, supported natively on Linux (other metrics function cross-platform).

## 2. Running Benchmarks

### 2. Fetch the standard corpus

Clones shallow copies (`--depth 1`) of seven diverse open-source repositories:

```bash
./benchmark.sh --clone
```

### 3. Execute benchmark suite

Spawns an isolated px0 server process per repository, records metrics, and terminates the instance:

```bash
./benchmark.sh
```

## 3. Benchmark Corpus

The seven repositories were chosen to span two orders of magnitude in size and represent diverse language ecosystems:

| Repository                                            | Primary Language | Character / Role in Benchmark                                  |
| ----------------------------------------------------- | ---------------- | -------------------------------------------------------------- |
| [flask](https://github.com/pallets/flask)             | Python           | Compact library: tests instant sub-millisecond path.           |
| [redis](https://github.com/redis/redis)               | C                | Medium-sized C codebase with large monolithic source files.    |
| [react](https://github.com/facebook/react)             | JavaScript       | Deep directory nesting and many nested `.gitignore` files.     |
| [django](https://github.com/django/django)           | Python           | Large framework with thousands of modules and test suites.     |
| [TypeScript](https://github.com/microsoft/TypeScript) | TypeScript       | Very large source files with a massive generated baseline tree.|
| [kubernetes](https://github.com/kubernetes/kubernetes)| Go               | Large Go monorepo with extensive vendored code.                |
| [linux](https://github.com/torvalds/linux)            | C                | The extreme case: ~95,000 files and 1.8 GB of source text.     |

## 4. Benchmark Results

Measured on Linux x86_64 with language servers disabled (`-no-lsp`):

| Repo       | Source Size | Files  | Index  | Fuzzy   | Full Scan | Open Big | Reopen | Base Mem | Peak Mem |
| ---------- | ----------- | ------ | ------ | ------- | --------- | -------- | ------ | -------- | -------- |
| django     | 74 MB       | 7,014  | 39 ms  | 1.3 ms  | 26.8 ms   | 166.8 ms | 1.0 ms | 20 MB    | 29 MB    |
| flask      | 3 MB        | 235    | 1 ms   | 0.8 ms  | 2.3 ms    | n/a      | n/a    | 16 MB    | 18 MB    |
| kubernetes | 370 MB      | 25,926 | 150 ms | 13.5 ms | 84.6 ms   | 199.0 ms | 0.9 ms | 30 MB    | 44 MB    |
| linux      | 1,809 MB    | 95,710 | 370 ms | 6.0 ms  | 451.8 ms  | 26.7 ms  | 0.6 ms | 55 MB    | 73 MB    |
| react      | 63 MB       | 7,178  | 52 ms  | 2.7 ms  | 32.2 ms   | 57.7 ms  | 0.7 ms | 21 MB    | 28 MB    |
| redis      | 26 MB       | 1,855  | 13 ms  | 1.0 ms  | 18.2 ms   | 80.8 ms  | 1.2 ms | 17 MB    | 27 MB    |
| typescript | 414 MB      | 66,533 | 566 ms | 6.2 ms  | 150.3 ms  | 40.9 ms  | 6.5 ms | 69 MB    | 105 MB   |

### Metric Descriptions

- `Source`: Total working tree size (excluding `.git`).
- `Files`: Number of indexed files after applying `.gitignore` and built-in rules.
- `Index`: Cold startup directory walk and ignore set construction time.
- `Fuzzy`: Time to fuzzy-match a query across every indexed path.
- `Full Scan`: Literal search for a term that matches nothing (worst-case full codebase scan reading every byte).
- `Open Big`: Cold open of the largest source file: read, tokenize viewport window, return HTML.
- `Reopen`: Opening the same file once cached in memory.
- `Base Mem`: Resident memory (RSS) after indexing.
- `Peak Mem`: Peak memory during aggressive search and navigation prior to idle scavenging.

## 5. px0 vs. Editors Comparison

Side-by-side comparison on identical Linux hardware across px0 and several other IDE/Editors, focusing on architectural weight and responsiveness constraints:

### Multi-Editor Benchmark Matrix

| Editor | Architecture / Process Model | Host / Server RSS | Total System RAM (incl. UI) | Time to Open |
| :--- | :--- | :--- | :--- | :--- |
| **px0** | Native Go daemon + Browser client | **~20 - 30 MB** | **~100 - 180 MB** | **~10 ms** |
| Vim | Native CLI | ~10 - 15 MB | ~10 - 15 MB | ~15 ms |
| Neovim | Native CLI | ~10 - 20 MB | ~10 - 20 MB | ~150 ms |
| Zed | Native GUI (Metal / Vulkan) | ~200 - 450 MB | ~200 - 450 MB | *GUI dependent* |
| Sublime Text | Native GUI (C++) | ~100 - 250 MB | ~100 - 250 MB | *GUI dependent* |
| VS Code | Electron (Chromium + Node) | ~1,100 - 1,440 MB | ~1,100 - 1,440 MB | ~3.0 - 5.0 s |

*Note: CLI editors (Vim/Neovim) do not provide inline LSP out-of-the-box (like px0 does) without extra processes. Zed and Sublime Text were evaluated as active running GUI configurations. px0 serves a full workspace complete with instantaneous indexing natively in 20-30 Megabytes.*

### Accounting for the Browser Tab (Client/Server Breakdown)

A fair and rigorous evaluation of px0 requires acknowledging its client-server architecture:
1. **Host Go Server**: A single static native binary running on the workspace host (~20–30 MB RSS).
2. **Web Client**: A browser tab running in an existing web browser (Chrome, Firefox, Safari) providing the virtualized UI (~80–150 MB RSS).

Because desktop Electron IDEs (such as VS Code or Cursor) bundle Chromium and Node.js directly into their process tree, comparing only px0's Go server against VS Code's combined tree would be an incomplete comparison without accounting for the browser tab.

#### Client/Server Footprint Breakdown vs. VS Code

| Component / Layer | VS Code (Desktop Electron) | VS Code Remote (`code-server`) | px0 (Local Mode) | px0 (Remote Server Mode) |
| :--- | :--- | :--- | :--- | :--- |
| **Server / Host Daemon** | ~400–600 MB *(Node.js, Extension Host)* | ~500–1,200 MB *(VS Code Server tree)* | **~20–30 MB** *(Native Go binary)* | **~20–30 MB** *(Host memory only)* |
| **Client UI / Frontend** | ~700–900 MB *(Bundled Chromium + GPU)* | ~150–300 MB *(Web browser tab)* | **~80–150 MB** *(Single browser tab)* | **~80–150 MB** *(Local client browser)* |
| **Total System RAM** | **~1,100–1,440 MB** | **~650–1,500 MB** | **~100–180 MB** *(~85–90% reduction)* | **~100–180 MB** |
| **Host Impact (Server / Devbox)** | N/A | ~500–1,200 MB | ~20–30 MB | **~20–30 MB** |

#### Why this architectural distinction matters:

1. **Total System Memory is Still ~90% Lighter**:
   Even when adding the browser tab (~80–150 MB) to the Go backend (~20–30 MB), px0's total local system footprint is **~100–180 MB**. Compared to Electron-based IDEs running at ~1,100–1,440 MB, px0 achieves an **85–90% net memory reduction** across the operating system.
2. **Remote & Cloud Devboxes**:
   When working across remote servers, cloud VMs, Kubernetes pods, or devboxes (`px0 -host 0.0.0.0`), the remote machine pays **strictly the ~20–30 MB server cost**. The UI rendering workload is offloaded to the developer's local machine. In contrast, remote solutions like `code-server` run heavy Node.js runtimes and remote daemons directly on the server, consuming 500 MB to 1.2 GB+ of server memory.
3. **Marginal Cost of an Existing Browser**:
   Developers virtually always have a browser running with active tabs. Adding one lightweight tab to an already-warm browser process pool avoids the steep CPU and memory penalty of cold-starting a dedicated, isolated Chromium instance, GPU process, and helper daemons.
4. **Virtualized DOM Keeps the Tab Lean**:
   px0 does not bundle heavy third-party editor frameworks like Monaco or CodeMirror. The frontend uses a custom virtualized renderer that mounts only ~60 active rows at any time regardless of file size. As a result, the browser tab itself remains bounded (~80–150 MB) and does not balloon when inspecting 500,000-line files or large diffs.

### Measured VS Code Process Tree Breakdown (Baseline Contrast)

VS Code's Electron-based standard serves as a useful baseline for modern IDE abstraction costs. Measuring its process footprint highlights how heavy typical environments become:

```text
PID     Role / Component                 RSS (MB)   CPU %
388357  Extension Host                   345.5 MB   4.0%
388724  Language Server (Pyrefly)        288.4 MB   0.0%
388075  VS Code Server Main              146.1 MB   0.0%
388113  File Watcher                     67.9 MB    0.0%
388089  IPC / Socket Proxy               64.7 MB    0.0%
388715  LSP: JSON Language Server        63.0 MB    0.0%
388697  PTY Host (Terminal)              62.8 MB    0.0%
...
```

In contrast, px0 embeds real-time indexing, fuzzy search, syntax highlighting, language-routing, and server endpoints inside a single, zero-dependency native process.

## 6. Memory Scavenging Verification

To observe resident memory scavenging in real time, run:

```bash
./benchmark.sh --memory bench-repos/linux
```

```text
### linux
  after indexing                     59 MB
  after a fuzzy find                 59 MB
  after 5 full-tree searches         85 MB
  after opening the largest file     88 MB
  after scrolling through it         94 MB
  8 seconds idle                     94 MB
  30 seconds idle                    57 MB
```

After 15 seconds of inactivity, px0 triggers `debug.FreeOSMemory()`, returning unused heap pages back to the Linux kernel and settling back to baseline.

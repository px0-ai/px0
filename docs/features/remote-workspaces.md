# Remote Workspaces & Cloud Inspection

px0 is architected from the ground up as a remote-first code reading environment. It allows you to run a single binary on any remote server, cloud instance, Docker container, or CI runner and browse the codebase directly in your local desktop browser without SSH keys, port forwarding setups, or heavy remote extension daemons.

---

## Overview & Core Purpose

Modern software development frequently takes place across cloud instances (AWS EC2, GCP Compute Engine), remote containers, Kubernetes pods, and devboxes. Traditional remote development setups (such as VS Code Remote-SSH, remote X11 forwarding, or VNC) require multi-step authentication setups, background server daemon installations, high CPU overhead, and significant network bandwidth.

px0 simplifies remote code inspection into a single shell command. Because the entire application—Go backend, HTTP server, assets, and frontend—is compiled into one static ~9.5 MB binary with zero external dependencies, you can copy px0 to any remote Linux, macOS, or BSD machine and spin it up instantly. Connecting via Tailscale, WireGuard, private VPCs, or reverse proxies gives you a fluid, graphical code inspection console in your browser.

---

## Key Capabilities

- **Zero Remote Daemons**: No Node.js runtime, no npm packages, no Electron layers, and no background extension churn on the remote machine.
- **Single Port Operation**: px0 serves all assets, JSON APIs, and search queries over a single HTTP port (default `7777`).
- **Flexible Network Binding**:
  - Bind to localhost for private tunnels (`-host 127.0.0.1`).
  - Bind to all interfaces for Tailscale/VPN access (`-host 0.0.0.0`).
- **Headless Server Mode (`-no-open`)**: Starts the server silently on remote machines or in Docker containers without attempting to invoke a local web browser.
- **Built-in Security & Sandboxing**:
  - **Path Traversal Protection**: Enforces strict path sandboxing; requests attempting to escape the workspace root using `../` or symlink cycle attacks are immediately blocked.
  - **DNS Rebinding Defense**: Inspects incoming HTTP `Host` headers to prevent cross-site scripting attacks via malicious DNS records.
  - **Agent Security Restrictions**: Agent editing commands are permitted only over direct IP addresses or `localhost`. Access over external hostnames or tunnel proxies automatically disables agent modifications to protect remote machines.
- **Direct Terminal Ergonomics**: Launch px0 targeting specific files or line numbers directly from the command line:
  - `px0` (opens current directory)
  - `px0 ~/projects/kernel` (opens specified repository)
  - `px0 main.go:42` (opens directly to line 42)
- **Built-in Self-Updater (`px0 --update`)**: Checks GitHub releases and seamlessly upgrades the single binary in place.

---

## Developer Workflows & Setup Patterns

### 1. Cloud Devbox via Tailscale or WireGuard
Run px0 on your cloud instance bound to all interfaces:
```bash
px0 -host 0.0.0.0 -port 7777 ~/work/repo
```
Open your local browser to `http://100.x.y.z:7777` (your machine's private Tailscale IP). You get full code reading, fuzzy search, and diff inspection with zero SSH lag.

### 2. Ephemeral Docker Container Inspection
Inspect code inside a running container or test environment:
```bash
docker run -p 7777:7777 -v $(pwd):/workspace px0:latest -host 0.0.0.0 /workspace
```

### 3. CI/CD Runner Debugging
When a build or test suite fails on a remote CI runner, download px0, run it in the background, and inspect generated artifacts, failure logs, and git status directly in your browser.

### 4. Reverse Proxy & Subpath Hosting (`-base-path`)
When hosting px0 behind a reverse proxy (Nginx, Traefik, Caddy), an API gateway, or a multi-tenant cloud platform (such as PR review pods at `https://tenant.px0.ai/rev-123/` or internal portals at `https://corp.internal/tools/px0/`), px0 is served from a URL subpath rather than the root domain (`/`).

Run px0 with `-base-path`:
```bash
px0 -base-path /rev-123/ -host 0.0.0.0 -port 7777 ~/workspace
```

**When to use `-base-path`:**
- **Hosted/Multi-Tenant Review Platforms**: When each PR review environment runs in an isolated container/pod routed through an edge gateway under a subpath (e.g. `/rev-<id>/`).
- **Path-Based Ingress Routing**: When routing traffic through an existing domain or Kubernetes Ingress where the root `/` is reserved for another service.
- **Corporate Dev Portals & Reverse Proxies**: When proxying multiple developer tools behind paths like `/tools/code-review/`.

**What `-base-path` does:**
- Prefixes all Go HTTP multiplexer endpoints (`/<base-path>/api/...`, `/<base-path>/static/...`).
- Dynamically injects `<base href="/<base-path>/">` into the served `index.html`, allowing the browser to resolve all relative asset requests, WebSocket/SSE connections, and API calls correctly.
- Automatically handles redirects: requests to `/<base-path>` without a trailing slash redirect to `/<base-path>/`, and root `/` redirects to the configured base path.

---

## CLI Flag Reference

| Flag | Default | Description |
| :--- | :--- | :--- |
| `-base-path P` | `"/"` | Base URL path prefix to serve endpoints and assets from (e.g. `/rev-123/`). Also configurable in settings via `server.basePath`. |
| `-port N` | `7777` | Port to listen on (`0` picks an ephemeral free port) |
| `-host H` | `127.0.0.1` | Network address to bind |
| `-no-open` | `false` | Suppress automatic browser launch (ideal for servers) |
| `-no-lsp` | `false` | Disable Language Server discovery |
| `-no-git` | `false` | Disable Git status checks and diff viewing |
| `-agent H` | none | Pin active coding agent harness for session |
| `-no-agent` | `false` | Disable coding agent editing features entirely |
| `-verbose` | `false` | Log every HTTP request, searches, symbols, and agent prompts to terminal |
| `-quiet` | `false` | Suppress CLI narration on stdout |
| `-update` | `false` | Check for updates and install latest release |
| `-version` | `false` | Print version and architecture and exit |

---

## Technical Architecture Deep Dive

For an architectural breakdown of HTTP routing, gzip connection pooling, symlink cycle immunity, and memory scavenging pipelines, see [System Architecture & Runtime Lifecycle Internals](../internals/architecture.md).

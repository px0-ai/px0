package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// lspServerDef describes one language server we know how to drive. Nothing here
// is required for px0 to work; a server is used only if its binary is found on
// PATH or in one of the folders installers commonly use (lspBinDirs).
type lspServerDef struct {
	Name        string
	Lang        string // language name, shown when offering to install the server
	Cmd         []string
	Exts        []string          // file extensions this server handles
	LangIDs     map[string]string // ext -> LSP languageId, when it differs
	DefaultLang string
	InitOptions map[string]any
	Install     []lspInstall // ways to get the binary, best first
}

func (d lspServerDef) LanguageID(rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	if id, ok := d.LangIDs[ext]; ok {
		return id
	}
	return d.DefaultLang
}

// lspRegistry is ordered: the first entry whose binary exists wins for a given
// extension, so a more capable server listed earlier takes precedence.
var lspRegistry = []lspServerDef{
	{
		Name: "gopls", Lang: "Go", Cmd: []string{"gopls"},
		Exts: []string{".go"}, DefaultLang: "go",
		Install: []lspInstall{
			{Cmd: []string{"go", "install", "golang.org/x/tools/gopls@latest"}, Auto: true},
			{OS: "darwin", Cmd: []string{"brew", "install", "gopls"}, Auto: true},
		},
	},
	{
		Name: "rust-analyzer", Lang: "Rust", Cmd: []string{"rust-analyzer"},
		Exts: []string{".rs"}, DefaultLang: "rust",
		Install: []lspInstall{
			{Cmd: []string{"rustup", "component", "add", "rust-analyzer"}, Auto: true},
			{OS: "darwin", Cmd: []string{"brew", "install", "rust-analyzer"}, Auto: true},
		},
	},
	{
		Name: "pyright", Lang: "Python", Cmd: []string{"pyright-langserver", "--stdio"},
		Exts: []string{".py", ".pyi"}, DefaultLang: "python",
		Install: []lspInstall{
			{Cmd: []string{"npm", "install", "-g", "pyright"}, Auto: true},
			{OS: "darwin", Cmd: []string{"brew", "install", "pyright"}, Auto: true},
		},
	},
	{
		Name: "pylsp", Lang: "Python", Cmd: []string{"pylsp"},
		Exts: []string{".py", ".pyi"}, DefaultLang: "python",
		Install: []lspInstall{
			{Cmd: []string{"pipx", "install", "python-lsp-server"}, Auto: true},
			{OS: "darwin", Cmd: []string{"brew", "install", "python-lsp-server"}, Auto: true},
		},
	},
	{
		// Not offered for install: it lints, but answers no call hierarchy.
		Name: "ruff", Lang: "Python", Cmd: []string{"ruff", "server"},
		Exts: []string{".py"}, DefaultLang: "python",
	},
	{
		Name: "typescript", Lang: "TypeScript and JavaScript", Cmd: []string{"typescript-language-server", "--stdio"},
		Exts:        []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		LangIDs:     map[string]string{".ts": "typescript", ".tsx": "typescriptreact", ".jsx": "javascriptreact"},
		DefaultLang: "javascript",
		Install: []lspInstall{
			{Cmd: []string{"npm", "install", "-g", "typescript-language-server", "typescript"}, Auto: true},
			{OS: "darwin", Cmd: []string{"brew", "install", "typescript-language-server"}, Auto: true},
		},
	},
	{
		Name: "clangd", Lang: "C and C++", Cmd: []string{"clangd", "--background-index"},
		Exts:        []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".m", ".mm"},
		LangIDs:     map[string]string{".c": "c", ".h": "c"},
		DefaultLang: "cpp",
		Install: []lspInstall{
			{OS: "darwin", Cmd: []string{"brew", "install", "llvm"}, Auto: true},
			// System package managers want an administrator: shown, not run.
			{OS: "linux", Cmd: []string{"sudo", "apt", "install", "clangd"}},
			{OS: "windows", Cmd: []string{"winget", "install", "LLVM.LLVM"}},
		},
	},
	{
		Name: "zls", Lang: "Zig", Cmd: []string{"zls"},
		Exts: []string{".zig"}, DefaultLang: "zig",
		Install: []lspInstall{
			{OS: "darwin", Cmd: []string{"brew", "install", "zls"}, Auto: true},
		},
	},
	{
		Name: "lua", Lang: "Lua", Cmd: []string{"lua-language-server"},
		Exts: []string{".lua"}, DefaultLang: "lua",
		Install: []lspInstall{
			{OS: "darwin", Cmd: []string{"brew", "install", "lua-language-server"}, Auto: true},
		},
	},
	{
		Name: "solargraph", Lang: "Ruby", Cmd: []string{"solargraph", "stdio"},
		Exts: []string{".rb"}, DefaultLang: "ruby",
		Install: []lspInstall{
			{Cmd: []string{"gem", "install", "solargraph"}, Auto: true},
			{OS: "darwin", Cmd: []string{"brew", "install", "solargraph"}, Auto: true},
		},
	},
	{
		Name: "jdtls", Lang: "Java", Cmd: []string{"jdtls"},
		Exts: []string{".java"}, DefaultLang: "java",
		Install: []lspInstall{
			{OS: "darwin", Cmd: []string{"brew", "install", "jdtls"}, Auto: true},
		},
	},
	{
		Name: "omnisharp", Lang: "C#", Cmd: []string{"omnisharp", "-lsp"},
		Exts: []string{".cs"}, DefaultLang: "csharp",
	},
	{
		Name: "texlab", Lang: "LaTeX", Cmd: []string{"texlab"},
		Exts: []string{".tex"}, DefaultLang: "latex",
		Install: []lspInstall{
			{OS: "darwin", Cmd: []string{"brew", "install", "texlab"}, Auto: true},
		},
	},
}

// ---------------------------------------------------------------- manager

type lspState string

const (
	lspOff      lspState = "off"      // disabled, or no server installed for this type
	lspStarting lspState = "starting" // process spawned, handshake in flight
	lspIndexing lspState = "indexing" // up, but still chewing through the workspace
	lspReady    lspState = "ready"
	lspFailed   lspState = "failed"
)

// maxLSPRestarts bounds how often one crashed language server is respawned.
const maxLSPRestarts = 3

// lspManager coordinates background language servers across file types and extensions.
// It manages on-demand server lazy starting, discovery, restart on crash, installation jobs,
// and path allowlisting for external references (such as standard library files).
type lspManager struct {
	root    string
	enabled bool

	mu        sync.Mutex
	byExt     map[string]*lspServerDef // resolved by discover(), again on Rescan
	clients   map[string]*lspClient    // server name -> client
	starting  map[string]chan struct{}
	failed    map[string]string
	available []string
	restarts  map[string]int // crashes recovered from, per server name
	stopped   map[string]bool

	discovered bool // the first discover() has finished

	jobMu sync.Mutex
	jobs  map[string]*lspJob // install runs, by server name

	// external holds absolute paths outside the indexed tree that a language
	// server pointed us at. Only these are openable beyond the root, so a
	// jump into the standard library works without opening up the filesystem.
	extMu    sync.Mutex
	external map[string]bool
}

func (m *lspManager) allow(abs string) {
	m.extMu.Lock()
	if m.external == nil {
		m.external = map[string]bool{}
	}
	if len(m.external) < 20000 {
		m.external[abs] = true
	}
	m.extMu.Unlock()
}

// Allowed reports whether a language server has named this exact file.
func (m *lspManager) Allowed(abs string) bool {
	m.extMu.Lock()
	defer m.extMu.Unlock()
	return m.external[abs]
}

// newLSPManager creates a new language server manager for root.
// If enabled is true, server discovery begins in the background without blocking startup.
func newLSPManager(root string, enabled bool) *lspManager {
	m := &lspManager{
		root: root, enabled: enabled,
		byExt:    map[string]*lspServerDef{},
		clients:  map[string]*lspClient{},
		starting: map[string]chan struct{}{},
		failed:   map[string]string{},
		stopped:  map[string]bool{},
	}
	if !enabled {
		return m
	}
	// Discover available language servers in background so server startup is instantaneous.
	go m.discover()
	return m
}

// discover resolves which registry servers are installed. It can run again
// (Rescan) after something is installed; the result replaces the previous one
// in a single swap, so readers never see a half-built table.
func (m *lspManager) discover() {
	dirs := lspBinDirs()
	byExt := map[string]*lspServerDef{}
	var available []string
	for i := range lspRegistry {
		def := lspRegistry[i] // a copy: Cmd[0] becomes the resolved path
		bin, ok := lookPathIn(def.Cmd[0], dirs)
		if !ok {
			continue
		}
		def.Cmd = append([]string{bin}, def.Cmd[1:]...)
		claimed := false
		for _, ext := range def.Exts {
			if byExt[ext] == nil {
				byExt[ext] = &def
				claimed = true
			}
		}
		if claimed {
			available = append(available, def.Name)
		}
	}
	m.mu.Lock()
	m.byExt, m.available, m.discovered = byExt, available, true
	m.mu.Unlock()
}

func (m *lspManager) isDiscovered() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.discovered
}

// lspBinDirs lists folders installers put binaries in that are often missing
// from PATH, so a server installed from px0, or by hand after px0 started, is
// found without restarting the shell px0 was launched from.
func lspBinDirs() []string {
	var dirs []string
	add := func(elem ...string) { dirs = append(dirs, filepath.Join(elem...)) }
	if v := os.Getenv("GOBIN"); v != "" {
		add(v)
	}
	for _, p := range filepath.SplitList(os.Getenv("GOPATH")) {
		if p != "" {
			add(p, "bin")
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(home, "go", "bin")     // go install, default GOPATH
		add(home, ".cargo", "bin") // rustup, cargo install
		add(home, ".local", "bin") // pipx
		add(home, ".opencode", "bin")
		add(home, ".codex", "bin")
	}
	// npm puts global packages beside its own executable unless the prefix was moved.
	if npm, err := exec.LookPath("npm"); err == nil {
		add(filepath.Dir(npm))
	}
	switch runtime.GOOS {
	case "darwin":
		add("/opt/homebrew/bin")
		add("/usr/local/bin")
		// Homebrew's llvm is keg-only: clangd is installed but never linked.
		add("/opt/homebrew/opt/llvm/bin")
		add("/usr/local/opt/llvm/bin")
	case "windows":
		if v := os.Getenv("APPDATA"); v != "" {
			add(v, "npm")
		}
		if v := os.Getenv("ProgramFiles"); v != "" {
			add(v, "LLVM", "bin")
		}
	}
	return dirs
}

// lookPathIn finds a command on PATH, then in dirs. On Windows exec.LookPath
// also tries PATHEXT extensions for a joined path, so "npm" finds npm.cmd.
func lookPathIn(name string, dirs []string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, d := range dirs {
		if p, err := exec.LookPath(filepath.Join(d, name)); err == nil {
			return p, true
		}
	}
	return "", false
}

func (m *lspManager) Available() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.available == nil {
		return []string{}
	}
	cp := make([]string, len(m.available))
	copy(cp, m.available)
	return cp
}

// Enabled reports whether language server support is on for this session
// (-no-lsp turns it off process-wide).
func (m *lspManager) Enabled() bool { return m.enabled }

// memBytes returns the combined resident memory of every currently running
// language server process, best-effort: a server whose RSS can't be read
// (exited, unsupported platform, no permission) contributes 0.
func (m *lspManager) memBytes() uint64 {
	m.mu.Lock()
	pids := make([]int, 0, len(m.clients))
	for _, c := range m.clients {
		if c.cmd != nil && c.cmd.Process != nil && c.alive() == nil {
			pids = append(pids, c.cmd.Process.Pid)
		}
	}
	m.mu.Unlock()

	var total uint64
	for _, pid := range pids {
		total += readRSSForPID(pid)
	}
	return total
}

func (m *lspManager) defFor(rel string) *lspServerDef {
	if !m.enabled {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byExt[strings.ToLower(filepath.Ext(rel))]
}

// State reports what a caller can expect for this file without starting
// anything, so the UI can say "indexing" instead of silently showing regex hits.
func (m *lspManager) State(rel string) (lspState, string) {
	m.mu.Lock()
	if m.stopped != nil {
		for _, reg := range registryFor(rel) {
			if m.stopped[reg.Name] {
				m.mu.Unlock()
				return lspOff, reg.Name
			}
		}
	}
	m.mu.Unlock()

	def := m.defFor(rel)
	if def == nil {
		// Discovery runs in the background at startup. Until it finishes, a
		// file type px0 knows may still get a server, so keep the UI asking.
		if m.enabled && !m.isDiscovered() && len(registryFor(rel)) > 0 {
			return lspStarting, ""
		}
		return lspOff, ""
	}
	m.mu.Lock()
	if m.stopped != nil && m.stopped[def.Name] {
		m.mu.Unlock()
		return lspOff, def.Name
	}
	c, ok := m.clients[def.Name]
	why, bad := m.failed[def.Name]
	_, pending := m.starting[def.Name]
	m.mu.Unlock()

	switch {
	case bad:
		return lspFailed, why
	case pending:
		return lspStarting, def.Name
	case !ok:
		return lspStarting, def.Name // not spawned yet; the next call will
	case c.alive() != nil:
		return lspFailed, def.Name
	case c.busy():
		return lspIndexing, def.Name
	}
	return lspReady, def.Name
}

// client returns a started client for rel, spawning one on first use. Callers
// that cannot wait should pass a short context; the spawn continues regardless
// so the next request finds it ready.
func (m *lspManager) client(ctx context.Context, rel string) (*lspClient, error) {
	def := m.defFor(rel)
	if def == nil {
		return nil, errNoServer
	}
	m.mu.Lock()
	if m.stopped != nil && m.stopped[def.Name] {
		m.mu.Unlock()
		return nil, errStopped
	}
	m.mu.Unlock()
	for {
		m.mu.Lock()
		if why, bad := m.failed[def.Name]; bad {
			m.mu.Unlock()
			return nil, errFailed{why}
		}
		if c, ok := m.clients[def.Name]; ok {
			err := c.alive()
			if err == nil {
				m.mu.Unlock()
				return c, nil
			}
			// A crashed server would otherwise take hover, definitions and
			// references down with it for the rest of the session. Start a
			// fresh one, but stop after a few crashes so a server that dies on
			// every request is not respawned forever.
			if m.restarts == nil {
				m.restarts = map[string]int{}
			}
			if m.restarts[def.Name] >= maxLSPRestarts {
				m.mu.Unlock()
				return nil, err
			}
			m.restarts[def.Name]++
			delete(m.clients, def.Name)
			m.mu.Unlock()
			go c.shutdown()
			continue
		}
		if wait, ok := m.starting[def.Name]; ok {
			m.mu.Unlock()
			select {
			case <-wait:
				continue // loop back and pick up the result
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		m.starting[def.Name] = done
		m.mu.Unlock()

		go m.spawn(def, done)

		select {
		case <-done:
			continue
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (m *lspManager) spawn(def *lspServerDef, done chan struct{}) {
	// The handshake gets a generous budget of its own: some servers do real
	// work before answering initialize.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := newLSPClient(*def, m.root)
	err := c.start(ctx)

	m.mu.Lock()
	if err != nil {
		m.failed[def.Name] = err.Error()
	} else {
		m.clients[def.Name] = c
	}
	delete(m.starting, def.Name)
	m.mu.Unlock()
	close(done)
}

func (m *lspManager) CloseDoc(abs, rel string) {
	def := m.defFor(rel)
	if def == nil {
		return
	}
	m.mu.Lock()
	c := m.clients[def.Name]
	m.mu.Unlock()
	if c != nil {
		c.closeDoc(abs)
	}
}

func (m *lspManager) EnsureOpen(ctx context.Context, abs, rel string) error {
	c, err := m.client(ctx, rel)
	if err != nil {
		return err
	}
	return c.ensureOpen(abs, rel)
}

func (m *lspManager) SyncDoc(abs, rel string) error {
	def := m.defFor(rel)
	if def == nil {
		return nil
	}
	m.mu.Lock()
	c := m.clients[def.Name]
	m.mu.Unlock()
	if c != nil {
		return c.syncDoc(abs, rel)
	}
	return nil
}

func (m *lspManager) RefreshOpenDocs() {
	m.mu.Lock()
	clients := make([]*lspClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.mu.Unlock()

	for _, c := range clients {
		c.mu.Lock()
		opened := make([]string, 0, len(c.opened))
		for uri := range c.opened {
			opened = append(opened, uri)
		}
		c.mu.Unlock()

		for _, uri := range opened {
			abs, err := uriToPath(uri)
			if err != nil {
				continue
			}
			if _, err := os.Stat(abs); os.IsNotExist(err) {
				c.closeDoc(abs)
				continue
			}
			rel, err := filepath.Rel(m.root, abs)
			if err != nil {
				rel = abs
			}
			rel = filepath.ToSlash(rel)
			_ = c.syncDoc(abs, rel)
		}
	}
}

func (m *lspManager) Close() {
	m.mu.Lock()
	clients := make([]*lspClient, 0, len(m.clients))
	for _, c := range m.clients {
		clients = append(clients, c)
	}
	m.clients = map[string]*lspClient{}
	m.mu.Unlock()
	for _, c := range clients {
		c.shutdown()
	}
}

type errFailed struct{ why string }

func (e errFailed) Error() string { return e.why }

var (
	errNoServer = errFailed{"no language server for this file type"}
	errStopped  = errFailed{"language server stopped"}
)

// Stop stops the named server process, or all running servers if name is empty or "all".
// The server is marked stopped to prevent auto-spawning until explicitly restarted.
func (m *lspManager) Stop(name string) {
	m.mu.Lock()
	if m.stopped == nil {
		m.stopped = map[string]bool{}
	}
	if name == "" || name == "all" {
		for _, def := range lspRegistry {
			m.stopped[def.Name] = true
		}
		clients := make([]*lspClient, 0, len(m.clients))
		for _, c := range m.clients {
			clients = append(clients, c)
		}
		m.clients = map[string]*lspClient{}
		m.mu.Unlock()
		for _, c := range clients {
			c.shutdown()
		}
		return
	}

	m.stopped[name] = true
	c := m.clients[name]
	delete(m.clients, name)
	m.mu.Unlock()
	if c != nil {
		c.shutdown()
	}
}

// StartServer unmarks the server as stopped and spawns the background language server process.
func (m *lspManager) StartServer(name string) error {
	if !m.enabled {
		return fmt.Errorf("lsp disabled")
	}
	m.mu.Lock()
	if m.stopped != nil {
		delete(m.stopped, name)
	}
	if m.failed != nil {
		delete(m.failed, name)
	}
	m.mu.Unlock()

	var targetDef *lspServerDef
	m.mu.Lock()
	for _, def := range m.byExt {
		if def.Name == name {
			targetDef = def
			break
		}
	}
	m.mu.Unlock()

	if targetDef == nil {
		dirs := lspBinDirs()
		for i := range lspRegistry {
			if lspRegistry[i].Name == name {
				def := lspRegistry[i]
				if bin, ok := lookPathIn(def.Cmd[0], dirs); ok {
					def.Cmd = append([]string{bin}, def.Cmd[1:]...)
					targetDef = &def
				}
				break
			}
		}
	}

	if targetDef == nil {
		return fmt.Errorf("language server %q is not installed", name)
	}

	m.mu.Lock()
	if c, ok := m.clients[name]; ok && c.alive() == nil {
		m.mu.Unlock()
		return nil
	}
	if _, ok := m.starting[name]; ok {
		m.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	m.starting[name] = done
	m.mu.Unlock()

	go m.spawn(targetDef, done)
	return nil
}

// AnyRunning reports whether at least one language server client is actively running or starting.
func (m *lspManager) AnyRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		if c.alive() == nil {
			return true
		}
	}
	return len(m.starting) > 0
}

func shortLangName(lang string) string {
	switch lang {
	case "TypeScript and JavaScript":
		return "TS / JS"
	case "TypeScript":
		return "TS"
	case "JavaScript":
		return "JS"
	case "C and C++":
		return "C / C++"
	default:
		return lang
	}
}

// ServerStatus returns the status summary of a language server definition.
func (m *lspManager) ServerStatus(def *lspServerDef) map[string]any {
	m.mu.Lock()
	c, ok := m.clients[def.Name]
	_, starting := m.starting[def.Name]
	why, failed := m.failed[def.Name]
	isStopped := m.stopped != nil && m.stopped[def.Name]
	m.mu.Unlock()

	dirs := lspBinDirs()
	_, installed := lookPathIn(def.Cmd[0], dirs)

	var opts []map[string]any
	autoIndex := -1
	for i, in := range def.installsFor(runtime.GOOS) {
		_, has := lookPathIn(in.Cmd[0], dirs)
		if in.Auto && has && autoIndex == -1 {
			autoIndex = i
		}
		opts = append(opts, map[string]any{
			"index":   i,
			"cmd":     strings.Join(in.Cmd, " "),
			"auto":    in.Auto,
			"tool":    in.Cmd[0],
			"hasTool": has,
		})
	}

	res := map[string]any{
		"name":      def.Name,
		"lang":      shortLangName(def.Lang),
		"fullLang":  def.Lang,
		"exts":      def.Exts,
		"installed": installed,
		"running":   false,
		"state":     "stopped",
		"options":   opts,
		"autoOpt":   autoIndex,
	}

	if j := m.job(def.Name); j != nil {
		res["job"] = map[string]any{
			"running": j.Running,
			"cmd":     j.Cmd,
			"error":   j.Error,
		}
		if j.Running {
			res["state"] = "installing"
			return res
		}
		if j.Error != "" && !installed {
			res["state"] = "missing"
			res["error"] = j.Error
			return res
		}
	}

	if !installed {
		res["state"] = "missing"
		return res
	}

	if isStopped {
		res["state"] = "stopped"
		return res
	}

	if failed {
		res["state"] = "failed"
		res["error"] = why
		return res
	}

	if starting {
		res["state"] = "starting"
		res["running"] = true
		return res
	}

	if ok && c != nil && c.alive() == nil {
		res["running"] = true
		if c.busy() {
			res["state"] = "indexing"
		} else {
			res["state"] = "ready"
		}
		if c.cmd != nil && c.cmd.Process != nil {
			pid := c.cmd.Process.Pid
			res["pid"] = pid
			res["memBytes"] = readRSSForPID(pid)
		}
		return res
	}

	res["state"] = "stopped"
	return res
}

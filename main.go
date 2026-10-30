// Package main implements px0: a fast, local-first code navigator and review tool
// that opens any repository or pull request in a responsive browser UI.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/px0-ai/harness"

	"px0/internal/settings"
	"px0/internal/telemetry"
	"px0/internal/ui"
	"px0/internal/update"
)

//go:embed VERSION
var rawVersion string

var version = strings.TrimSpace(rawVersion)

// posthogKey can be injected at build time: go build -ldflags="-X main.posthogKey=phc_..."
var posthogKey string

func main() {
	var (
		port         = flag.Int("port", 7777, "port to listen on (0 picks a free one)")
		host         = flag.String("host", "127.0.0.1", "address to bind")
		noOpen       = flag.Bool("no-open", false, "do not launch a browser")
		noLSP        = flag.Bool("no-lsp", false, "do not use language servers, even if installed")
		noGit        = flag.Bool("no-git", false, "disable git awareness")
		showVer      = flag.Bool("version", false, "print version and exit")
		showVerShort = flag.Bool("v", false, "print version and exit (shorthand)")
		doUpdate     = flag.Bool("update", false, "check for and install latest version of px0")
		noUpdate     = flag.Bool("no-update", false, "do not auto-update px0 on startup")
		noColor      = flag.Bool("no-color", false, "disable colour output")
		quiet        = flag.Bool("quiet", false, "suppress narration")
		verbose      = flag.Bool("verbose", false, "log startup steps, requests, searches, symbols, and agent prompts to terminal")
		noTelemetry  = flag.Bool("no-telemetry", false, "disable anonymous usage telemetry")
		agentCmd     = flag.String("agent", "", "pin the coding harness used for edits (claude, gemini, cursor-agent, agy, opencode, codex, aider, goose, or a command template containing {prompt}); detected and chosen in the UI when omitted")
		noAgent      = flag.Bool("no-agent", false, "do not offer editing through a coding harness")
		_            = flag.Bool("y", false, "answer yes to prompts (deprecated; PRs are always opened without prompt)")
		_            = flag.Bool("yes", false, "answer yes to prompts (alias for -y)")
		basePathFlag = flag.String("base-path", "", "base URL path prefix to serve endpoints and assets from (e.g. /rev-123/)")
		wbChild      = flag.Bool("workbench-child", false, "internal: run as a workspace of the px0 workbench")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "px0 %s - a code navigator\n\nusage:\n  px0 [flags]                      open the workbench\n  px0 [flags] <file or directory>\n  px0 [flags] <pr-url>\n  px0 completion <shell>           print a bash, zsh, fish or powershell completion script\n\nflags:\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *noColor {
		f := false
		ui.ForcedColor = &f
	}
	if *quiet || *wbChild {
		ui.Quiet = true
	}
	if *verbose {
		ui.Verbose = true
	}
	if *noGit {
		gitDisabled = true
	}

	if *showVer || *showVerShort || (flag.NArg() == 1 && flag.Arg(0) == "version") {
		fmt.Printf("px0 %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	// Completion scripts go to stdout untouched, before telemetry or updates start.
	if flag.NArg() > 0 && flag.Arg(0) == "completion" {
		if err := runCompletion(os.Stdout, os.Stderr, flag.Args()[1:], flag.CommandLine); err != nil {
			fatal(err)
		}
		return
	}

	if *doUpdate {
		if err := update.RunSelfUpdate(version); err != nil {
			fatal(err)
		}
		return
	}

	// Without a target, px0 opens the workbench: a launcher for directories
	// and pull requests that runs each one as a child px0 (workbench.go).
	if flag.NArg() == 0 && !*wbChild {
		var childArgs []string
		for _, f := range []struct {
			on   bool
			flag string
		}{{*noLSP, "-no-lsp"}, {*noGit, "-no-git"}, {*noAgent, "-no-agent"}, {*noTelemetry, "-no-telemetry"}} {
			if f.on {
				childArgs = append(childArgs, f.flag)
			}
		}
		if *agentCmd != "" {
			childArgs = append(childArgs, "-agent", *agentCmd)
		}
		runWorkbench(*host, *port, *basePathFlag, !*noOpen, !*noUpdate, childArgs)
		return
	}

	// A full pull or merge request URL (see prURLExamples)
	// checks out the PR's full source tree instead of resolving a local file/directory.
	// Only full URLs via "px0 <url>" are supported for PR review.
	target := "."
	var prProvider GitProvider
	var prTarget PRTarget
	isPR := false
	if flag.NArg() > 0 {
		arg0 := flag.Arg(0)
		if arg0 == "pr" {
			fatal(fmt.Errorf("'px0 pr' is no longer supported; open pull requests directly with: px0 <url>"))
		}
		if provider, pt, ok := DetectPRURL(arg0); ok {
			prProvider, prTarget, isPR = provider, pt, true
		} else {
			target = arg0
		}
	}
	if isPR && gitDisabled {
		fatal(fmt.Errorf("px0: git is required for PR review; remove -no-git"))
	}

	var pr *prSession
	var root, initialFile string
	var initialLine int
	var targetDur time.Duration
	if isPR {
		sp := ui.NewSpinner(fmt.Sprintf("Preparing PR #%d (%s/%s)...", prTarget.Number, prTarget.Owner, prTarget.Repo), os.Stdout)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		// Outside a clone of the PR's repository, use one the workbench knows,
		// so the checkout is a worktree rather than a fresh clone.
		cloneDir := "."
		if top, _ := findPRRemote(".", prTarget.Owner, prTarget.Repo); top == "" {
			st := &wbStore{path: workbenchStorePath()}
			if known := knownClone(st.Load(), prTarget.Owner+"/"+prTarget.Repo); known != "" {
				cloneDir = known
			}
		}
		p, err := checkoutPR(ctx, prProvider, prTarget, cloneDir, func(msg string) {
			sp.Update(msg)
		})
		cancel()
		if err != nil {
			sp.Fail(fmt.Sprintf("Failed to prepare PR #%d: %v", prTarget.Number, err))
			fatal(fmt.Errorf("px0: %w", err))
		}
		if p.meta.Merged {
			sp.Success(fmt.Sprintf("PR #%d checked out [merged] (%s)", prTarget.Number, p.meta.Title))
		} else {
			sp.Success(fmt.Sprintf("PR #%d checked out (%s)", prTarget.Number, p.meta.Title))
		}
		pr = p
		root = p.Root()
	} else {
		tStart := time.Now()
		r, f, l, err := resolveTarget(target)
		if err != nil {
			fatal(err)
		}
		targetDur = time.Since(tStart)
		root, initialFile, initialLine = r, f, l
	}

	tListen := time.Now()
	ln, addr, err := listen(*host, *port)
	if err != nil {
		fatal(err)
	}
	listenDur := time.Since(tListen)

	tInit := time.Now()
	ix := NewIndex(root)
	lsp := newLSPManager(root, !*noLSP)
	tel := telemetry.New(posthogKey, version, *noTelemetry)
	defer tel.Close("normal")

	configuredBasePath := "/"
	if *basePathFlag != "" {
		configuredBasePath = cleanBasePath(*basePathFlag)
	} else if cfg := settings.Read(); cfg.ServerBasePath != nil && *cfg.ServerBasePath != "" {
		configuredBasePath = cleanBasePath(*cfg.ServerBasePath)
	}

	pxSrv := NewServer(ix, lsp, configuredBasePath)
	pxSrv.tel = tel
	if *wbChild {
		pxSrv.workbenchURL = workbenchBaseOf(configuredBasePath)
	} else if pr != nil {
		it := wbPRItem(prTarget)
		it.Title = pr.meta.Title
		go recordRecent(it)
	} else {
		go recordRecent(wbDirItem(root))
	}
	if pr != nil {
		pxSrv.SetPR(pr)
	}
	var agent *agentManager
	if !*noAgent {
		agent, err = newAgentManager(root, *agentCmd, lsp)
		if err != nil {
			fatal(fmt.Errorf("-agent: %w", err))
		}
		pxSrv.SetAgent(agent)
	}
	initDur := time.Since(tInit)

	srv := &http.Server{Handler: pxSrv}
	srv.RegisterOnShutdown(pxSrv.EndStreams)
	if *wbChild {
		// The workbench reads this line for the address it proxies to.
		fmt.Printf("%s%s\n", wbReadyPrefix, addr)
	}

	url := viewerURL(addr, initialFile, initialLine, configuredBasePath)
	ui.Heading("px0 "+version, nil, os.Stdout)
	if pr != nil {
		prTitle := fmt.Sprintf("#%d %s", pr.meta.Number, pr.meta.Title)
		if pr.meta.Merged {
			prTitle += " " + ui.Paint("[MERGED]", ui.ColorWarn, true, os.Stdout)
		}
		ui.KV("PR", prTitle, 11, os.Stdout)
		if pr.token == "" {
			ui.KV("access", ui.Dim(fmt.Sprintf("read-only (no %s token: %s to submit reviews)", pr.provider.Info().Label, pr.provider.Info().TokenHint), os.Stdout), 11, os.Stdout)
		}
	}
	ui.KV("workspace", root, 11, os.Stdout)
	ui.KV("url", ui.Accent(url, os.Stdout), 11, os.Stdout)
	if *host == "0.0.0.0" {
		for _, networkURL := range networkURLs(addr, initialFile, initialLine) {
			ui.KV("network", ui.Accent(networkURL, os.Stdout), 11, os.Stdout)
		}
	}
	ui.Hint("ctrl-c to stop", os.Stdout)

	if ui.Verbose {
		if !isPR {
			ui.Status("ok", "resolved workspace target", ui.FmtDuration(targetDur), 0, os.Stdout)
		}
		ui.Status("ok", fmt.Sprintf("bound TCP listener on %s", addr), ui.FmtDuration(listenDur), 0, os.Stdout)
		ui.Status("ok", "initialized HTTP server and services", ui.FmtDuration(initDur), 0, os.Stdout)
	}

	// Launch browser immediately without blocking startup.
	if !*noOpen {
		tBrowser := time.Now()
		go openBrowser(url)
		if ui.Verbose {
			ui.Status("ok", "spawned browser launcher", ui.FmtDuration(time.Since(tBrowser)), 0, os.Stdout)
		}
	}

	// Index workspace asynchronously so the server and UI respond in <1ms.
	go func() {
		ix.Build()
		if pxSrv.gitWatcher != nil {
			pxSrv.gitWatcher.Trigger()
		}
		n, _, ms := ix.Stats()
		ui.Status("ok", fmt.Sprintf("indexed %d files", n), fmt.Sprintf("%dms", ms), 0, os.Stdout)
		tLSP := time.Now()
		names := lsp.Available()
		lspDur := time.Since(tLSP)
		if len(names) > 0 {
			if ui.Verbose {
				ui.Status("ok", fmt.Sprintf("discovered language servers: %s", strings.Join(names, ", ")), ui.FmtDuration(lspDur), 0, os.Stdout)
			} else {
				ui.Bullet(fmt.Sprintf("language servers: %s (started on first use)", strings.Join(names, ", ")), os.Stdout)
			}
		} else if ui.Verbose {
			ui.Status("ok", "checked language servers (none installed)", ui.FmtDuration(lspDur), 0, os.Stdout)
		}
		if agent != nil {
			tAgent := time.Now()
			var found []string
			for _, h := range agent.Detect() {
				if h.Installed {
					item := h.Name
					if h.Model != "" {
						item = fmt.Sprintf("%s (%s)", h.Name, h.Model)
					}
					found = append(found, item)
				}
			}
			agentDur := time.Since(tAgent)
			if ui.Verbose {
				if len(found) > 0 {
					ui.Status("ok", fmt.Sprintf("detected coding harnesses: %s", strings.Join(found, ", ")), ui.FmtDuration(agentDur), 0, os.Stdout)
				} else {
					ui.Status("ok", "checked coding harnesses (none found)", ui.FmtDuration(agentDur), 0, os.Stdout)
				}
			}
		}

		tel.Track("session_started", map[string]any{
			"files_bucket": telemetry.FilesBucket(n),
			"index_ms":     ms,
			"has_git":      gitAvailable(root),
			"has_lsp":      len(lsp.Available()) > 0,
		})
	}()

	// Check for updates in the background once the server is already up, so
	// px0 never makes a network call before serving the first request.
	if !*noUpdate {
		go update.AutoUpdate(version)
	}

	// Language servers are children that can hold gigabytes. Shut them down on
	// the way out rather than leaving them for the OS to reap.
	interrupted := false
	shutDown := make(chan struct{}) // closes once Shutdown has drained every request
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	if *wbChild {
		// The workbench holds our stdin open; it closes when the workbench
		// stops this workspace or exits for any reason.
		go func() {
			io.Copy(io.Discard, os.Stdin)
			stop <- syscall.SIGTERM
		}()
	}
	go func() {
		<-stop
		interrupted = true
		fmt.Print("\r")
		ui.Status("info", "px0 stopped", "", 0, os.Stderr)
		go func() {
			<-stop // Second interrupt forces immediate exit
			os.Exit(130)
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(shutDown)
	}()

	err = srv.Serve(ln)
	if err == http.ErrServerClosed {
		// Serve returns as soon as Shutdown starts; exiting then would cut
		// the responses it is still draining.
		<-shutDown
	}
	lsp.Close()
	agent.Close()
	pxSrv.CloseThreads()
	pr.Close()

	if interrupted {
		tel.Close("interrupted")
		os.Exit(130)
	}

	tel.Close("normal")
	if err != nil && err != http.ErrServerClosed {
		fatal(err)
	}
}

// runWorkbench serves the workbench until interrupted, then stops every
// workspace it started.
func runWorkbench(host string, port int, basePathFlag string, open, doAutoUpdate bool, childArgs []string) {
	if u := runningWorkbench(); u != "" {
		ui.Status("ok", "workbench already running", u, 0, os.Stdout)
		if open {
			openBrowser(u)
		}
		return
	}
	exe, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	basePath := "/"
	if basePathFlag != "" {
		basePath = cleanBasePath(basePathFlag)
	} else if cfg := settings.Read(); cfg.ServerBasePath != nil && *cfg.ServerBasePath != "" {
		basePath = cleanBasePath(*cfg.ServerBasePath)
	}

	ln, addr, err := listen(host, port)
	if err != nil {
		fatal(err)
	}
	wb := newWorkbench(basePath, cwd, exe, childArgs)
	srv := &http.Server{Handler: wb}

	u := viewerURL(addr, "", 0, basePath)
	_, boundPort, _ := net.SplitHostPort(addr)
	writeWorkbenchLock(viewerURL(net.JoinHostPort("127.0.0.1", boundPort), "", 0, basePath))
	defer removeWorkbenchLock()

	ui.Heading("px0 "+version, nil, os.Stdout)
	ui.KV("workbench", ui.Accent(u, os.Stdout), 11, os.Stdout)
	if host == "0.0.0.0" {
		for _, networkURL := range networkURLs(addr, "", 0) {
			ui.KV("network", ui.Accent(networkURL, os.Stdout), 11, os.Stdout)
		}
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		ui.Status("warn", "anyone who can reach this address can browse and open any directory on this machine", "", 0, os.Stdout)
	}
	ui.Hint("ctrl-c to stop", os.Stdout)
	if open {
		go openBrowser(u)
	}
	if doAutoUpdate {
		go update.AutoUpdate(version)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	interrupted := false
	go func() {
		<-stop
		interrupted = true
		fmt.Print("\r")
		ui.Status("info", "px0 stopped", "", 0, os.Stderr)
		go func() {
			<-stop
			os.Exit(130)
		}()
		// Children first: their open event streams would hold Shutdown up.
		wb.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	err = srv.Serve(ln)
	wb.Close()
	if interrupted {
		removeWorkbenchLock()
		os.Exit(130)
	}
	if err != nil && err != http.ErrServerClosed {
		removeWorkbenchLock()
		fatal(err)
	}
}

// workbenchBaseOf turns a workspace's base path (/<wb>/w/<id>/) back into the
// workbench's own.
func workbenchBaseOf(childBase string) string {
	trimmed := strings.TrimSuffix(childBase, "/")
	if i := strings.LastIndex(trimmed, "/w/"); i >= 0 {
		return trimmed[:i+1]
	}
	return "/"
}

// resolveTarget turns a directory or file into a workspace root, along with an optional
// initial file to open and optional line number.
// If the target is inside a git repository, that repository root is used as the workspace root.
// Otherwise, for relative paths within the current working directory, the working directory
// is used. Standalone files fall back to their parent directory.
func resolveTarget(target string) (root, initialFile string, initialLine int, err error) {
	cleanedTarget, line := splitTargetLine(target)
	abs, err := filepath.Abs(cleanedTarget)
	if err != nil {
		return "", "", 0, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid target %s: %w", abs, err)
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid target %s: %w", abs, err)
	}
	if st.IsDir() {
		return resolved, "", 0, nil
	}
	if !st.Mode().IsRegular() {
		return "", "", 0, fmt.Errorf("not a regular file or directory: %s", abs)
	}

	parentDir := filepath.Dir(resolved)

	// If the file is inside a git repository, use the git repo root.
	if info := gitProbe(parentDir); info.ok && info.toplevel != "" {
		if rel, err := filepath.Rel(info.toplevel, resolved); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return info.toplevel, filepath.ToSlash(rel), line, nil
		}
	}

	// If the target was specified as a relative path within the current working directory,
	// use the current working directory as the workspace root.
	if !filepath.IsAbs(cleanedTarget) {
		if wd, err := os.Getwd(); err == nil {
			if resolvedWd, err := filepath.EvalSymlinks(wd); err == nil {
				if rel, err := filepath.Rel(resolvedWd, resolved); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
					return resolvedWd, filepath.ToSlash(rel), line, nil
				}
			}
		}
	}

	return parentDir, filepath.Base(resolved), line, nil
}

// splitTargetLine separates trailing :line or :line:col from target if the candidate path exists.
func splitTargetLine(target string) (path string, line int) {
	if _, err := os.Stat(target); err == nil {
		return target, 0
	}
	lastColon := strings.LastIndex(target, ":")
	if lastColon <= 0 {
		return target, 0
	}
	vol := filepath.VolumeName(target)
	if lastColon <= len(vol) {
		return target, 0
	}
	suffix := target[lastColon+1:]
	num, err := strconv.Atoi(suffix)
	if err != nil || num <= 0 {
		return target, 0
	}
	rest := target[:lastColon]
	secondColon := strings.LastIndex(rest, ":")
	if secondColon > len(vol) {
		secondSuffix := rest[secondColon+1:]
		if lineNum, err := strconv.Atoi(secondSuffix); err == nil && lineNum > 0 {
			candidate := rest[:secondColon]
			if _, err := os.Stat(candidate); err == nil {
				return candidate, lineNum
			}
		}
	}
	if _, err := os.Stat(rest); err == nil {
		return rest, num
	}
	return target, 0
}

func viewerURL(addr, initialFile string, initialLine int, basePath ...string) string {
	u := url.URL{Scheme: "http", Host: addr}
	bp := "/"
	if len(basePath) > 0 && basePath[0] != "" {
		bp = cleanBasePath(basePath[0])
	}
	if bp != "/" {
		u.Path = bp
	}
	q := u.Query()
	if initialFile != "" {
		q.Set("path", filepath.ToSlash(initialFile))
	}
	if initialLine > 0 {
		q.Set("line", strconv.Itoa(initialLine))
	}
	if len(q) > 0 {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// networkURLs returns URLs for the machine's non-loopback IPv4 addresses.
// The supplied address contributes the bound port; its host is intentionally
// ignored because a bind address is not necessarily a usable destination.
func networkURLs(addr, initialFile string, initialLine int) []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	return networkURLsFromAddrs(addr, initialFile, initialLine, addrs)
}

func networkURLsFromAddrs(addr, initialFile string, initialLine int, addrs []net.Addr) []string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	urls := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		var ip net.IP
		switch a := addr.(type) {
		case *net.IPNet:
			ip = a.IP
		case *net.IPAddr:
			ip = a.IP
		default:
			continue
		}

		ip = ip.To4()
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			continue
		}

		host := ip.String()
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		urls = append(urls, viewerURL(net.JoinHostPort(host, port), initialFile, initialLine))
	}

	sort.Strings(urls)
	return urls
}

// listen binds the requested port, walking forward if it is already taken so a
// second instance does not simply fail. If a block of ports is busy, it falls
// back to an OS-assigned free port.
func listen(host string, port int) (net.Listener, string, error) {
	if port == 0 {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			return nil, "", err
		}
		return ln, ln.Addr().String(), nil
	}
	for p := port; p < port+100; p++ {
		addr := net.JoinHostPort(host, fmt.Sprint(p))
		if ln, err := net.Listen("tcp", addr); err == nil {
			return ln, addr, nil
		}
	}
	// Fallback to any free port assigned by the OS if port range is busy
	if ln, err := net.Listen("tcp", net.JoinHostPort(host, "0")); err == nil {
		return ln, ln.Addr().String(), nil
	}
	return nil, "", fmt.Errorf("no free port available starting from %d", port)
}

func openBrowser(url string) {
	// If BROWSER environment variable is set, try that first
	if b := os.Getenv("BROWSER"); b != "" {
		if cmd := exec.Command(b, url); cmd.Start() == nil {
			return
		}
	}

	var cmds []*exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmds = []*exec.Cmd{exec.Command("open", url)}
	case "windows":
		cmds = []*exec.Cmd{
			exec.Command("rundll32", "url.dll,FileProtocolHandler", url),
			exec.Command("cmd.exe", "/c", "start", url),
		}
	default:
		// On Linux/Unix, detect WSL to open the browser on the Windows host seamlessly
		if isWSL() {
			// WSL2's localhost port-forwarding relay needs a moment to notice a
			// freshly bound listener before Windows can reach it. Without this,
			// the browser can open before the relay catches up and shows a
			// connection error until the next reload.
			time.Sleep(500 * time.Millisecond)
			cmds = append(cmds,
				exec.Command("wslview", url),
				exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Process", fmt.Sprintf(`"%s"`, url)),
				exec.Command("cmd.exe", "/c", "start", "", url),
			)
		}

		// On Linux/Unix desktop, try xdg-open, sensible-browser, gio, or common browsers
		cmds = append(cmds,
			exec.Command("xdg-open", url),
			exec.Command("sensible-browser", url),
			exec.Command("gio", "open", url),
			exec.Command("google-chrome", url),
			exec.Command("firefox", url),
			exec.Command("chromium", url),
		)
	}

	for _, cmd := range cmds {
		if err := cmd.Start(); err == nil {
			return
		}
	}
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err == nil && (strings.Contains(strings.ToLower(string(data)), "microsoft") || strings.Contains(strings.ToLower(string(data)), "wsl")) {
		return true
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "px0:", err)
	os.Exit(1)
}

// completionShells are the shells "px0 completion <shell>" writes a script for.
var completionShells = []string{"bash", "zsh", "fish", "powershell"}

// completionCommands are the words px0 accepts in place of a target.
var completionCommands = []completionCommand{
	{"completion", "print a shell completion script"},
	{"version", "print version and exit"},
}

// completionCommand is one command as the completion scripts offer it.
type completionCommand struct{ name, desc string }

// completionHidden are flags left out of completion: internal or deprecated.
var completionHidden = map[string]bool{"workbench-child": true, "y": true, "yes": true}

// completionFlag is one flag as the completion scripts offer it.
type completionFlag struct {
	name   string
	desc   string
	value  bool     // takes a value (-port 7777) rather than standing alone (-no-lsp)
	values []string // fixed choices for the value, if any
}

// completionFlags lists the visible flags of fs, in the order flag.VisitAll
// gives (sorted by name), so new flags reach the scripts without extra work.
func completionFlags(fs *flag.FlagSet) []completionFlag {
	var out []completionFlag
	fs.VisitAll(func(f *flag.Flag) {
		if completionHidden[f.Name] {
			return
		}
		cf := completionFlag{name: f.Name, desc: completionDesc(f.Usage), value: true}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			cf.value = false
		}
		if f.Name == "agent" {
			cf.values = harness.PresetNames()
		}
		out = append(out, cf)
	})
	return out
}

// completionDesc shortens a flag's usage to the clause before any
// parenthetical or semicolon, which is all a completion menu has room for.
func completionDesc(usage string) string {
	if i := strings.IndexAny(usage, "(;"); i > 0 {
		usage = usage[:i]
	}
	return strings.TrimSpace(usage)
}

// printCompletion writes the completion script for shell to w. The script is
// rebuilt from the binary on every call, so sourcing it at shell startup
// (rather than saving it) keeps it in step with self-updates.
func printCompletion(w io.Writer, shell string, fs *flag.FlagSet, descriptions bool) error {
	flags := completionFlags(fs)
	cmds := append([]completionCommand(nil), completionCommands...)
	if !descriptions {
		for i := range flags {
			flags[i].desc = ""
		}
		for i := range cmds {
			cmds[i].desc = ""
		}
	}
	switch shell {
	case "bash":
		writeBashCompletion(w, flags, cmds)
	case "zsh":
		writeZshCompletion(w, flags, cmds)
	case "fish":
		writeFishCompletion(w, flags, cmds)
	case "powershell":
		writePowerShellCompletion(w, flags, cmds)
	default:
		return fmt.Errorf("unsupported shell %q (supported: %s)", shell, strings.Join(completionShells, ", "))
	}
	return nil
}

// runCompletion handles "px0 completion [-no-descriptions] <shell>".
func runCompletion(stdout, stderr io.Writer, args []string, fs *flag.FlagSet) error {
	cfs := flag.NewFlagSet("completion", flag.ContinueOnError)
	cfs.SetOutput(stderr)
	noDesc := cfs.Bool("no-descriptions", false, "leave descriptions out of the completion menu")
	cfs.Usage = func() { fmt.Fprint(stderr, completionUsage) }
	if err := cfs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if cfs.NArg() != 1 {
		cfs.Usage()
		return fmt.Errorf("expected one shell (%s)", strings.Join(completionShells, ", "))
	}
	return printCompletion(stdout, cfs.Arg(0), fs, !*noDesc)
}

const completionUsage = `usage: px0 completion [-no-descriptions] <bash|zsh|fish|powershell>

Prints a shell completion script for px0. Load it from your shell's startup
file so it is regenerated whenever px0 updates itself:

  bash        add to ~/.bashrc:     eval "$(px0 completion bash)"
  zsh         add to ~/.zshrc:      source <(px0 completion zsh)
              (after compinit runs: autoload -Uz compinit && compinit)
  fish        run once:             px0 completion fish > ~/.config/fish/completions/px0.fish
  powershell  add to $PROFILE:      px0 completion powershell | Out-String | Invoke-Expression

Start a new shell afterwards. If you save the script to a file instead
(for example as _px0 on zsh's $fpath), regenerate it after updating px0.
`

// valueFlags returns the names of flags that take a value, with and without
// fixed choices, for scripts that complete them differently.
func valueFlags(flags []completionFlag) (free, fixed []completionFlag) {
	for _, f := range flags {
		switch {
		case len(f.values) > 0:
			fixed = append(fixed, f)
		case f.value:
			free = append(free, f)
		}
	}
	return free, fixed
}

// bashAlts joins the flags' -name and --name spellings as a case pattern.
func bashAlts(flags []completionFlag) string {
	var alts []string
	for _, f := range flags {
		alts = append(alts, "-"+f.name, "--"+f.name)
	}
	return strings.Join(alts, "|")
}

func writeBashCompletion(w io.Writer, flags []completionFlag, cmds []completionCommand) {
	free, fixed := valueFlags(flags)
	cmdNames := make([]string, len(cmds))
	for i, c := range cmds {
		cmdNames[i] = c.name
	}
	fmt.Fprintf(w, "# bash completion for px0. Load with: eval \"$(px0 completion bash)\"\n\n")
	fmt.Fprintf(w, "_px0() {\n")
	fmt.Fprintf(w, "    local cur=\"${COMP_WORDS[COMP_CWORD]}\" prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	fmt.Fprintf(w, "    COMPREPLY=()\n")
	fmt.Fprintf(w, "    if [[ $COMP_CWORD -eq 2 && $prev == completion ]]; then\n")
	fmt.Fprintf(w, "        COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(completionShells, " "))
	fmt.Fprintf(w, "        return\n    fi\n")
	// px0 stops parsing flags at its target, so offer none after one.
	fmt.Fprintf(w, "    local i\n")
	fmt.Fprintf(w, "    for ((i = 1; i < COMP_CWORD; i++)); do\n")
	fmt.Fprintf(w, "        case \"${COMP_WORDS[i]}\" in\n")
	if len(free)+len(fixed) > 0 {
		fmt.Fprintf(w, "            %s) ((i++)); if [[ ${COMP_WORDS[i]} == = ]]; then ((i++)); fi ;;\n", bashAlts(append(free, fixed...)))
	}
	fmt.Fprintf(w, "            -*) ;;\n")
	fmt.Fprintf(w, "            *) return ;;\n")
	fmt.Fprintf(w, "        esac\n")
	fmt.Fprintf(w, "    done\n")
	fmt.Fprintf(w, "    local prefix=\"\"\n")
	fmt.Fprintf(w, "    if [[ \"$cur\" == -*=* ]]; then\n")
	fmt.Fprintf(w, "        prev=\"${cur%%%%=*}\"; prefix=\"$prev=\"; cur=\"${cur#*=}\"\n")
	fmt.Fprintf(w, "    elif [[ \"$prev\" == = && $COMP_CWORD -ge 2 ]]; then\n")
	fmt.Fprintf(w, "        prev=\"${COMP_WORDS[COMP_CWORD-2]}\"\n")
	fmt.Fprintf(w, "    elif [[ \"$cur\" == = ]]; then\n")
	fmt.Fprintf(w, "        cur=\"\"\n")
	fmt.Fprintf(w, "    fi\n")
	fmt.Fprintf(w, "    case \"$prev\" in\n")
	for _, f := range fixed {
		fmt.Fprintf(w, "        -%s|--%s)\n", f.name, f.name)
		fmt.Fprintf(w, "            COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(f.values, " "))
		fmt.Fprintf(w, "            if [[ -n $prefix ]]; then COMPREPLY=(\"${COMPREPLY[@]/#/$prefix}\"); fi\n")
		fmt.Fprintf(w, "            return ;;\n")
	}
	if len(free) > 0 {
		// A free-form value (a port, a host): offer nothing rather than flags.
		fmt.Fprintf(w, "        %s)\n", bashAlts(free))
		fmt.Fprintf(w, "            return ;;\n")
	}
	fmt.Fprintf(w, "    esac\n")
	fmt.Fprintf(w, "    if [[ \"$cur\" == -* ]]; then return; fi\n")
	// The target slot also takes a command; list matching ones beside the
	// paths, since a match would otherwise hide them.
	fmt.Fprintf(w, "    COMPREPLY=($(compgen -W %q -- \"$cur\"))\n", strings.Join(cmdNames, " "))
	fmt.Fprintf(w, "    if [[ ${#COMPREPLY[@]} -gt 0 ]]; then\n")
	fmt.Fprintf(w, "        local IFS=$'\\n'\n")
	fmt.Fprintf(w, "        COMPREPLY+=($(compgen -f -- \"$cur\"))\n")
	fmt.Fprintf(w, "    fi\n")
	// An empty COMPREPLY falls back to the shell's own path completion
	// (-o default), which handles spaces, ~ and trailing slashes correctly.
	fmt.Fprintf(w, "}\n\n")
	fmt.Fprintf(w, "complete -o default -o filenames -F _px0 px0\n")
}

// zshQuote escapes s for a single-quoted _arguments spec's [description].
func zshQuote(s string) string {
	return strings.NewReplacer(`'`, `'\''`, `[`, `\[`, `]`, `\]`, `:`, `\:`).Replace(s)
}

func writeZshCompletion(w io.Writer, flags []completionFlag, cmds []completionCommand) {
	fmt.Fprintf(w, "#compdef px0\n")
	fmt.Fprintf(w, "# zsh completion for px0. Load with: source <(px0 completion zsh)\n\n")
	fmt.Fprintf(w, "_px0() {\n")
	fmt.Fprintf(w, "  if (( CURRENT == 3 )) && [[ ${words[2]} == completion ]]; then\n")
	fmt.Fprintf(w, "    _values shell %s\n", strings.Join(completionShells, " "))
	fmt.Fprintf(w, "    return\n")
	fmt.Fprintf(w, "  fi\n")
	// -A '-*': px0 stops parsing flags at its target, so offer none after one.
	fmt.Fprintf(w, "  local context state state_descr line ret=1\n")
	fmt.Fprintf(w, "  typeset -A opt_args\n")
	fmt.Fprintf(w, "  _arguments -A '-*' \\\n")
	for _, f := range flags {
		spec := "!-" + f.name
		if f.value {
			spec += "="
		}
		if f.desc != "" {
			spec += "[" + zshQuote(f.desc) + "]"
		}
		switch {
		case len(f.values) > 0:
			spec += ":" + f.name + ":(" + strings.Join(f.values, " ") + ")"
		case f.value:
			spec += ":" + f.name + ": "
		}
		fmt.Fprintf(w, "    '%s' \\\n", spec)
		fmt.Fprintf(w, "    '%s' \\\n", strings.Replace(spec, "!-", "!--", 1))
	}
	fmt.Fprintf(w, "    '1: :->target' && ret=0\n")
	fmt.Fprintf(w, "  if [[ $state == target ]]; then\n")
	fmt.Fprintf(w, "    local -a commands=(\n")
	for _, c := range cmds {
		item := c.name
		if c.desc != "" {
			item += ":" + strings.ReplaceAll(c.desc, ":", `\:`)
		}
		fmt.Fprintf(w, "      '%s'\n", strings.ReplaceAll(item, "'", `'\''`))
	}
	fmt.Fprintf(w, "    )\n")
	fmt.Fprintf(w, "    _describe -t commands command commands && ret=0\n")
	fmt.Fprintf(w, "    _wanted files expl 'file, directory or pull request URL' _files && ret=0\n")
	fmt.Fprintf(w, "  fi\n")
	fmt.Fprintf(w, "  return ret\n")
	fmt.Fprintf(w, "}\n\n")
	// Works both autoloaded from $fpath (as _px0) and sourced after compinit.
	fmt.Fprintf(w, "if [ \"$funcstack[1]\" = \"_px0\" ]; then\n")
	fmt.Fprintf(w, "  _px0 \"$@\"\n")
	fmt.Fprintf(w, "else\n")
	fmt.Fprintf(w, "  compdef _px0 px0\n")
	fmt.Fprintf(w, "fi\n")
}

// fishQuote single-quotes s for fish, which escapes only ' and \ inside.
func fishQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

func writeFishCompletion(w io.Writer, flags []completionFlag, cmds []completionCommand) {
	fmt.Fprintf(w, "# fish completion for px0. Install with: px0 completion fish > ~/.config/fish/completions/px0.fish\n\n")
	fmt.Fprintf(w, "complete -c px0 -n '__fish_seen_subcommand_from completion' -f -a %s\n", fishQuote(strings.Join(completionShells, " ")))
	// No -f here: paths stay on offer next to the commands.
	for _, c := range cmds {
		line := "complete -c px0 -n __fish_use_subcommand -a " + fishQuote(c.name)
		if c.desc != "" {
			line += " -d " + fishQuote(c.desc)
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "function __px0_value_flag\n")
	fmt.Fprintf(w, "    set -l words (commandline -opc)\n")
	fmt.Fprintf(w, "    set -l pending ''\n")
	fmt.Fprintf(w, "    for word in $words[2..-1]\n")
	fmt.Fprintf(w, "        if test -n \"$pending\"\n")
	fmt.Fprintf(w, "            set pending ''\n")
	fmt.Fprintf(w, "            continue\n")
	fmt.Fprintf(w, "        end\n")
	fmt.Fprintf(w, "        switch $word\n")
	free, fixed := valueFlags(flags)
	for _, f := range append(free, fixed...) {
		fmt.Fprintf(w, "            case -%s --%s\n                set pending %s\n", f.name, f.name, fishQuote(f.name))
	}
	fmt.Fprintf(w, "            case '--'\n                return 1\n")
	fmt.Fprintf(w, "            case '-*'\n            case '*'\n                return 1\n")
	fmt.Fprintf(w, "        end\n    end\n")
	fmt.Fprintf(w, "    test \"$pending\" = \"$argv[1]\"; or string match -qr -- \"^--?$argv[1]=\" (commandline -ct)\n")
	fmt.Fprintf(w, "end\n")
	for _, f := range fixed {
		fmt.Fprintf(w, "complete -c px0 -n %s -o %s -x -a %s\n", fishQuote("__px0_value_flag "+f.name), f.name, fishQuote(strings.Join(f.values, " ")))
	}

}

// psQuote single-quotes s for PowerShell, which doubles ' inside.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func psList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = psQuote(s)
	}
	return "@(" + strings.Join(q, ", ") + ")"
}

func writePowerShellCompletion(w io.Writer, flags []completionFlag, cmds []completionCommand) {
	free, fixed := valueFlags(flags)
	var valueNames []string
	for _, f := range append(free, fixed...) {
		valueNames = append(valueNames, "-"+f.name, "--"+f.name)
	}
	fmt.Fprintf(w, "# powershell completion for px0. Load with: px0 completion powershell | Out-String | Invoke-Expression\n\n")
	fmt.Fprintf(w, "Register-ArgumentCompleter -Native -CommandName px0 -ScriptBlock {\n")
	fmt.Fprintf(w, "    param($wordToComplete, $commandAst, $cursorPosition)\n")
	fmt.Fprintf(w, "    $words = @($commandAst.CommandElements | Where-Object { $_.Extent.EndOffset -lt $cursorPosition } | ForEach-Object { $_.ToString() })\n")
	fmt.Fprintf(w, "    $prev = if ($words.Count -gt 1) { $words[-1] } else { '' }\n")
	fmt.Fprintf(w, "    $values = $null\n")
	for _, f := range fixed {
		fmt.Fprintf(w, "    if ($prev -eq %s -or $prev -eq %s) { $values = %s }\n", psQuote("-"+f.name), psQuote("--"+f.name), psList(f.values))
	}
	fmt.Fprintf(w, "    if ($words.Count -eq 2 -and $prev -eq 'completion') { $values = %s }\n", psList(completionShells))
	fmt.Fprintf(w, "    if ($values) {\n")
	fmt.Fprintf(w, "        $values | Where-Object { $_ -like \"$wordToComplete*\" } | ForEach-Object {\n")
	fmt.Fprintf(w, "            [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)\n")
	fmt.Fprintf(w, "        }\n")
	fmt.Fprintf(w, "        return\n")
	fmt.Fprintf(w, "    }\n")
	fmt.Fprintf(w, "    if ($wordToComplete -like '-*') { return }\n")
	// The target slot (the first word after the flags) also takes a command.
	fmt.Fprintf(w, "    $valueFlags = %s\n", psList(valueNames))
	fmt.Fprintf(w, "    for ($i = 1; $i -lt $words.Count; $i++) {\n")
	fmt.Fprintf(w, "        if ($valueFlags -contains $words[$i]) { $i++ } elseif ($words[$i] -notlike '-*') { return }\n")
	fmt.Fprintf(w, "    }\n")
	fmt.Fprintf(w, "    $commands = [ordered]@{\n")
	for _, c := range cmds {
		desc := c.desc
		if desc == "" {
			desc = c.name
		}
		fmt.Fprintf(w, "        %s = %s\n", psQuote(c.name), psQuote(desc))
	}
	fmt.Fprintf(w, "    }\n")
	fmt.Fprintf(w, "    $matched = @($commands.Keys | Where-Object { $_ -like \"$wordToComplete*\" })\n")
	fmt.Fprintf(w, "    if ($matched) {\n")
	fmt.Fprintf(w, "        $matched | ForEach-Object {\n")
	fmt.Fprintf(w, "            [System.Management.Automation.CompletionResult]::new($_, $_, 'Command', $commands[$_])\n")
	fmt.Fprintf(w, "        }\n")
	// Any result turns off the path fallback, so add the paths back.
	fmt.Fprintf(w, "        [System.Management.Automation.CompletionCompleters]::CompleteFilename($wordToComplete)\n")
	fmt.Fprintf(w, "    }\n")
	// Returning nothing lets PowerShell fall back to path completion.
	fmt.Fprintf(w, "}\n")
}

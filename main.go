package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

//go:embed VERSION
var rawVersion string

var version = strings.TrimSpace(rawVersion)

func main() {
	var (
		port         = flag.Int("port", 7777, "port to listen on (0 picks a free one)")
		host         = flag.String("host", "127.0.0.1", "address to bind")
		noOpen       = flag.Bool("no-open", false, "do not launch a browser")
		noLSP        = flag.Bool("no-lsp", false, "do not use language servers, even if installed")
		noGit        = flag.Bool("no-git", false, "disable git awareness")
		dev          = flag.String("dev", "", "serve the UI from this source directory instead of the embedded copy")
		showVer      = flag.Bool("version", false, "print version and exit")
		showVerShort = flag.Bool("v", false, "print version and exit (shorthand)")
		doUpdate     = flag.Bool("update", false, "check for and install latest version of px0")
		noColor      = flag.Bool("no-color", false, "disable colour output")
		quiet        = flag.Bool("quiet", false, "suppress narration")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "px0 %s - a code navigator\n\nusage: px0 [flags] [file or directory]\n\nflags:\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *noColor {
		f := false
		uiForcedColor = &f
	}
	if *quiet {
		uiQuiet = true
	}
	if *noGit {
		gitDisabled = true
	}

	if *showVer || *showVerShort || (flag.NArg() == 1 && flag.Arg(0) == "version") {
		fmt.Printf("px0 %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	if *doUpdate {
		if err := runSelfUpdate(version); err != nil {
			fatal(err)
		}
		return
	}

	if *dev != "" {
		if err := useDiskAssets(*dev); err != nil {
			fatal(fmt.Errorf("-dev %s: %w", *dev, err))
		}
	}

	target := "."
	if flag.NArg() > 0 {
		target = flag.Arg(0)
	}
	root, initialFile, err := resolveTarget(target)
	if err != nil {
		fatal(err)
	}

	ln, addr, err := listen(*host, *port)
	if err != nil {
		fatal(err)
	}

	ix := NewIndex(root)
	lsp := newLSPManager(root, !*noLSP)

	srv := &http.Server{Handler: NewServer(ix, lsp)}

	url := viewerURL(addr, initialFile)
	uiHeading("px0 "+version, nil, os.Stdout)
	uiKV("workspace", root, 11, os.Stdout)
	uiKV("url", uiAccent(url, os.Stdout), 11, os.Stdout)
	uiHint("ctrl-c to stop", os.Stdout)

	// Launch browser immediately without blocking startup.
	if !*noOpen {
		go openBrowser(url)
	}

	// Index workspace asynchronously so the server and UI respond in <1ms.
	go func() {
		ix.Build()
		n, _, ms := ix.Stats()
		uiStatus("ok", fmt.Sprintf("indexed %d files", n), fmt.Sprintf("%dms", ms), 0, os.Stdout)
		if names := lsp.Available(); len(names) > 0 {
			uiBullet(fmt.Sprintf("language servers: %s (started on first use)", strings.Join(names, ", ")), os.Stdout)
		}
	}()

	// Check for updates asynchronously once a day without delaying startup (<1ms).
	go checkDailyUpdate(version)

	// Language servers are children that can hold gigabytes. Shut them down on
	// the way out rather than leaving them for the OS to reap.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		fmt.Print("\r")
		uiStatus("warn", "interrupted", "", 0, os.Stderr)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		lsp.Close()
		os.Exit(130)
	}()

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		lsp.Close()
		fatal(err)
	}
	lsp.Close()
}

// resolveTarget turns a directory into a workspace root. For a regular file,
// its parent becomes the workspace and the file is opened after the UI loads.
func resolveTarget(target string) (root, initialFile string, err error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("invalid target %s: %w", abs, err)
	}
	st, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("invalid target %s: %w", abs, err)
	}
	if st.IsDir() {
		return resolved, "", nil
	}
	if !st.Mode().IsRegular() {
		return "", "", fmt.Errorf("not a regular file or directory: %s", abs)
	}
	return filepath.Dir(resolved), filepath.Base(resolved), nil
}

func viewerURL(addr, initialFile string) string {
	u := url.URL{Scheme: "http", Host: addr}
	if initialFile != "" {
		q := u.Query()
		q.Set("path", filepath.ToSlash(initialFile))
		u.RawQuery = q.Encode()
	}
	return u.String()
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

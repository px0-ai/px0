package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// remote mode: `px0 [user@]host:path` runs px0 on the remote bound to loopback
// and forwards its port here over ssh -L. The remote px0 lives as long as the
// ssh session: the launcher kills it when the session's stdin reaches EOF.

type remoteTarget struct {
	Dest string
	Path string
}

func parseRemoteTarget(target string) (remoteTarget, bool) {
	if target == "" {
		return remoteTarget{}, false
	}
	if _, err := os.Stat(target); err == nil {
		return remoteTarget{}, false
	}
	if filepath.VolumeName(target) != "" || strings.Contains(target, "://") {
		return remoteTarget{}, false
	}
	host, path, ok := strings.Cut(target, ":")
	if !ok || !validSSHDest(host) {
		return remoteTarget{}, false
	}
	if isDigits(path) {
		return remoteTarget{}, false // file.go:12 for a missing file
	}
	return remoteTarget{Dest: host, Path: path}, true
}

func validSSHDest(s string) bool {
	if s == "" || strings.HasPrefix(s, "-") || strings.Count(s, "@") > 1 {
		return false
	}
	user, host, hasUser := strings.Cut(s, "@")
	if !hasUser {
		host = s
	} else if user == "" || host == "" || strings.HasPrefix(host, "-") {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_', r == '@':
		default:
			return false
		}
	}
	return !isDigits(host)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

type remoteOptions struct {
	LocalPort   int
	NoOpen      bool
	Passthrough []string
	Stdout      io.Writer
	Stderr      io.Writer
	Stdin       io.Reader
	OpenBrowser func(url string)
	Ask         func(question string) bool
	FetchAsset  func(goos, goarch string) (io.ReadCloser, error)
}

var remoteBinCandidates = []string{
	"px0",
	`"$HOME/.local/bin/px0"`,
	`"$HOME/bin/px0"`,
	"/usr/local/bin/px0",
	"/opt/homebrew/bin/px0",
}

const remoteMissingMarker = "px0-remote: missing"

func remoteScript(path string, port int, passthrough []string) string {
	var b strings.Builder
	b.WriteString(`PX0=""; for c in ` + strings.Join(remoteBinCandidates, " ") + `; do if command -v "$c" >/dev/null 2>&1; then PX0=$(command -v "$c"); break; fi; done; `)
	b.WriteString(`if [ -z "$PX0" ]; then echo "` + remoteMissingMarker + ` $(uname -s) $(uname -m)"; exit 3; fi; `)
	b.WriteString(`"$PX0" -no-open -no-color -port ` + strconv.Itoa(port))
	for _, f := range passthrough {
		b.WriteString(" " + shellQuote(f))
	}
	if p := remotePathArg(path); p != "" {
		b.WriteString(" -- " + p)
	}
	// A background list reads /dev/null in non-interactive sh, so the session
	// stdin is duplicated onto fd 3 for the watcher.
	b.WriteString(` & p=$!; exec 3<&0; ( cat <&3 >/dev/null; kill "$p" 2>/dev/null ) >/dev/null 2>&1 & wait "$p"`)
	return b.String()
}

func remotePathArg(path string) string {
	switch {
	case path == "" || path == ".":
		return ""
	case path == "~", path == "~/":
		return `"$HOME"`
	case strings.HasPrefix(path, "~/"):
		return `"$HOME"/` + shellQuote(strings.TrimPrefix(path, "~/"))
	}
	return shellQuote(path)
}

const remoteCopyScript = `d="$HOME/.local/bin"; mkdir -p "$d" && cat > "$d/.px0.tmp" && chmod 755 "$d/.px0.tmp" && mv -f "$d/.px0.tmp" "$d/px0" && "$d/px0" -version`

func unameToGo(sys, machine string) (goos, goarch string) {
	switch strings.ToLower(sys) {
	case "linux", "darwin", "freebsd", "openbsd", "netbsd":
		goos = strings.ToLower(sys)
	}
	switch strings.ToLower(machine) {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	case "armv7l", "armv6l", "arm":
		goarch = "arm"
	case "i386", "i686":
		goarch = "386"
	case "riscv64":
		goarch = "riscv64"
	}
	return goos, goarch
}

var errRemotePortTaken = errors.New("remote port taken")

type errRemoteMissing struct{ sys, machine string }

func (e errRemoteMissing) Error() string {
	return fmt.Sprintf("px0 is not installed on the remote (%s %s)", e.sys, e.machine)
}

func runRemote(ctx context.Context, rt remoteTarget, opts remoteOptions) error {
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}
	if opts.Ask == nil {
		opts.Ask = func(q string) bool { return askYesNo(q, opts.Stdin, opts.Stdout) }
	}
	if opts.FetchAsset == nil {
		opts.FetchAsset = fetchReleaseAsset
	}
	sshBin, err := exec.LookPath("ssh")
	if err != nil {
		return errors.New("remote targets need the ssh client on PATH")
	}

	uiHeading("px0 "+version, nil, opts.Stdout)
	uiKV("remote", uiAccent(rt.Dest, opts.Stdout), 11, opts.Stdout)

	installed := false
	for attempt := 0; attempt < 4; attempt++ {
		err := runRemoteSession(ctx, sshBin, rt, opts)
		var missing errRemoteMissing
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errRemotePortTaken):
			uiStatus("warn", "remote port was busy, retrying", "", 0, opts.Stdout)
		case errors.As(err, &missing) && !installed:
			if err := installRemote(ctx, sshBin, rt, missing, opts); err != nil {
				return err
			}
			installed = true
		default:
			return err
		}
	}
	return errors.New("could not start px0 on the remote after several attempts")
}

func runRemoteSession(ctx context.Context, sshBin string, rt remoteTarget, opts remoteOptions) error {
	ln, addr, err := listen("127.0.0.1", opts.LocalPort)
	if err != nil {
		return err
	}
	_, portStr, _ := net.SplitHostPort(addr)
	localPort, _ := strconv.Atoi(portStr)
	ln.Close()

	remotePort := 20000 + rand.IntN(40000)
	args := append(sshArgs(rt, localPort, remotePort), "sh -c "+shellQuote(remoteScript(rt.Path, remotePort, opts.Passthrough)))

	cmd := exec.Command(sshBin, args...)
	cmd.Stderr = opts.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ssh: %w", err)
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			stdin.Close()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
		case <-done:
		}
	}()

	var sessionErr error
	gotURL := false
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, remoteMissingMarker):
			f := strings.Fields(strings.TrimPrefix(trimmed, remoteMissingMarker))
			m := errRemoteMissing{}
			if len(f) > 0 {
				m.sys = f[0]
			}
			if len(f) > 1 {
				m.machine = f[1]
			}
			sessionErr = m
		case strings.HasPrefix(trimmed, "url:"):
			remoteURL := strings.TrimSpace(strings.TrimPrefix(trimmed, "url:"))
			u, err := url.Parse(remoteURL)
			if err != nil || u.Port() != strconv.Itoa(remotePort) {
				sessionErr = errRemotePortTaken
				stdin.Close()
				continue
			}
			u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
			local := u.String()
			gotURL = true
			fmt.Fprintln(opts.Stdout, strings.Replace(line, remoteURL, uiAccent(local, opts.Stdout), 1))
			if !opts.NoOpen {
				go opts.OpenBrowser(local)
			}
		case strings.HasPrefix(trimmed, "px0 "):
			if v := strings.TrimPrefix(trimmed, "px0 "); v != version {
				fmt.Fprintf(opts.Stdout, "%s  %s\n", line, uiDim("(local "+version+")", opts.Stdout))
			}
		default:
			fmt.Fprintln(opts.Stdout, line)
		}
	}
	waitErr := cmd.Wait()
	close(done)

	if sessionErr != nil {
		return sessionErr
	}
	if waitErr != nil {
		// Ctrl-C reaches ssh too; give our own handler a moment to cancel ctx.
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if !gotURL {
		if waitErr != nil {
			return fmt.Errorf("remote px0 did not start (%v)", waitErr)
		}
		return errors.New("remote px0 exited before it published a url")
	}
	if waitErr != nil {
		return fmt.Errorf("ssh session ended: %v", waitErr)
	}
	return nil
}

func sshArgs(rt remoteTarget, localPort, remotePort int) []string {
	return []string{
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, remotePort),
		"--", rt.Dest,
	}
}

// installRemote puts the local version of px0 on the remote: the running
// binary when the platform matches, otherwise that platform's release asset.
func installRemote(ctx context.Context, sshBin string, rt remoteTarget, m errRemoteMissing, opts remoteOptions) error {
	goos, goarch := unameToGo(m.sys, m.machine)
	if goos == "" || goarch == "" {
		return fmt.Errorf("px0 is not installed on %s and its platform (%s %s) is not supported", rt.Dest, m.sys, m.machine)
	}
	if !opts.Ask(fmt.Sprintf("px0 is not installed on %s (%s/%s). Install px0 %s to ~/.local/bin there?", rt.Dest, goos, goarch, version)) {
		return fmt.Errorf("px0 is not installed on %s", rt.Dest)
	}

	var src io.ReadCloser
	if goos == runtime.GOOS && goarch == runtime.GOARCH {
		exe, err := os.Executable()
		if err == nil {
			exe, err = filepath.EvalSymlinks(exe)
		}
		if err != nil {
			return err
		}
		if src, err = os.Open(exe); err != nil {
			return err
		}
		uiStatus("step", fmt.Sprintf("copying px0 %s to %s", version, rt.Dest), "", 0, opts.Stdout)
	} else {
		uiStatus("step", fmt.Sprintf("downloading px0 %s for %s/%s", version, goos, goarch), "", 0, opts.Stdout)
		var err error
		if src, err = opts.FetchAsset(goos, goarch); err != nil {
			return err
		}
		uiStatus("step", "copying to "+rt.Dest, "", 0, opts.Stdout)
	}
	defer src.Close()

	cmd := exec.CommandContext(ctx, sshBin, "--", rt.Dest, "sh -c "+shellQuote(remoteCopyScript))
	cmd.Stdin = src
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install px0 on %s: %w", rt.Dest, err)
	}
	return nil
}

// fetchReleaseAsset downloads this version's release binary for another
// platform, verified against the release's checksums.txt.
func fetchReleaseAsset(goos, goarch string) (io.ReadCloser, error) {
	repo := getRepoName()
	asset := fmt.Sprintf("px0-%s-%s-%s", version, goos, goarch)
	base := fmt.Sprintf("https://github.com/%s/releases/download/v%s/", repo, version)
	tmp, err := os.CreateTemp("", "px0-remote-*")
	if err != nil {
		return nil, err
	}
	os.Remove(tmp.Name())
	client := &http.Client{Timeout: 5 * time.Minute}
	if err := downloadVerifiedAsset(client, base+asset, base+"checksums.txt", asset, tmp); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("%w; is px0 %s a published release?", err, version)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		tmp.Close()
		return nil, err
	}
	return tmp, nil
}

func askYesNo(question string, in io.Reader, out io.Writer) bool {
	if f, ok := in.(*os.File); ok && !isTTY(f) {
		return false
	}
	fmt.Fprintf(out, "%s %s [Y/n] ", uiGlyph("step", out), question)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		fmt.Fprintln(out)
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	}
	return false
}

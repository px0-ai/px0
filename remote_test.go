package main

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseRemoteTarget(t *testing.T) {
	dir := t.TempDir()
	withColon := filepath.Join(dir, "a:b")
	if err := os.MkdirAll(withColon, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   string
		want remoteTarget
		ok   bool
	}{
		{"vm:~/work/repo", remoteTarget{Dest: "vm", Path: "~/work/repo"}, true},
		{"deploy@10.0.0.7:/srv/app", remoteTarget{Dest: "deploy@10.0.0.7", Path: "/srv/app"}, true},
		{"build-box.internal:", remoteTarget{Dest: "build-box.internal", Path: ""}, true},
		{"vm:repo/main.go:42", remoteTarget{Dest: "vm", Path: "repo/main.go:42"}, true},
		{".", remoteTarget{}, false},
		{"", remoteTarget{}, false},
		{"main.go:12", remoteTarget{}, false},
		{"localhost:8080", remoteTarget{}, false},
		{withColon, remoteTarget{}, false},
		{"src/pkg:thing", remoteTarget{}, false},
		{"-oProxyCommand=x:path", remoteTarget{}, false},
		{"@vm:path", remoteTarget{}, false},
		{"a@b@c:path", remoteTarget{}, false},
		{":path", remoteTarget{}, false},
		{"ssh://vm/srv", remoteTarget{}, false},
	}
	for _, c := range cases {
		got, ok := parseRemoteTarget(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("parseRemoteTarget(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestRemoteScript(t *testing.T) {
	s := remoteScript("~/work/my repo", 41234, []string{"-no-lsp", "-agent", "claude --model x"})
	for _, want := range []string{
		`-port 41234`,
		`-no-open -no-color`,
		` -no-lsp -agent 'claude --model x'`,
		` -- "$HOME"/'work/my repo'`,
		remoteMissingMarker + ` $(uname -s) $(uname -m)"; exit 3`,
		`exec 3<&0; ( cat <&3 >/dev/null; kill "$p"`,
		`wait "$p"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	if s := remoteScript("", 1, nil); strings.Contains(s, " -- ") {
		t.Errorf("empty path should not pass an argument: %s", s)
	}
	if s := remoteScript("/srv/app", 1, nil); !strings.Contains(s, " -- /srv/app &") {
		t.Errorf("absolute path not passed plainly: %s", s)
	}
	if got := remotePathArg("~"); got != `"$HOME"` {
		t.Errorf("remotePathArg(~) = %s", got)
	}
	if got := remotePathArg("it's"); got != `'it'\''s'` {
		t.Errorf("remotePathArg quote = %s", got)
	}
}

func TestUnameToGo(t *testing.T) {
	cases := []struct{ sys, machine, goos, goarch string }{
		{"Linux", "x86_64", "linux", "amd64"},
		{"Linux", "aarch64", "linux", "arm64"},
		{"Darwin", "arm64", "darwin", "arm64"},
		{"FreeBSD", "amd64", "freebsd", "amd64"},
		{"Linux", "armv7l", "linux", "arm"},
		{"Plan9", "mips", "", ""},
	}
	for _, c := range cases {
		goos, goarch := unameToGo(c.sys, c.machine)
		if goos != c.goos || goarch != c.goarch {
			t.Errorf("unameToGo(%s, %s) = %s/%s, want %s/%s", c.sys, c.machine, goos, goarch, c.goos, c.goarch)
		}
	}
}

// fakeSSH puts a stand-in ssh and px0 on PATH. The ssh logs its arguments and
// runs the remote command locally through sh, so the launcher script is
// exercised for real. With missing set, the first session reports that px0 is
// absent as that platform; a copy session (remote command mentioning .px0.tmp)
// stores its stdin in the returned received path.
func fakeSSH(t *testing.T, missing string) (logPath, received string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake ssh needs sh")
	}
	dir := t.TempDir()
	logPath = filepath.Join(dir, "log")
	received = filepath.Join(dir, "received")
	state := filepath.Join(dir, "state")
	ssh := `#!/bin/sh
printf '%s\n' "$*" >> "` + logPath + `"
while [ $# -gt 0 ]; do
  case "$1" in
    -L|-o|-p) shift ;;
    --) shift; break ;;
  esac
  shift
done
shift
cmd="$*"
case "$cmd" in
  *.px0.tmp*) cat > "` + received + `"; echo "px0 9.9.9"; exit 0 ;;
esac
if [ -n "` + missing + `" ] && [ ! -f "` + state + `" ]; then
  : > "` + state + `"
  echo "` + remoteMissingMarker + ` ` + missing + `"
  exit 3
fi
sh -c "$cmd"
exit 130
`
	px0 := `#!/bin/sh
port=""
while [ $# -gt 0 ]; do case "$1" in -port) port="$2"; shift ;; esac; shift; done
echo
echo "px0 9.9.9"
echo "  workspace:  /srv/app"
echo "  url:        http://127.0.0.1:$port/?path=main.go"
echo
echo "ctrl-c to stop"
echo "[OK] indexed 3 files  1ms"
trap 'exit 130' TERM INT
while :; do sleep 1; done
`
	for name, body := range map[string]string{"ssh": ssh, "px0": px0} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath, received
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestRunRemoteForwardsAndStops(t *testing.T) {
	logPath, _ := fakeSSH(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut syncBuffer
	opened := make(chan string, 1)
	opts := remoteOptions{
		Passthrough: []string{"-no-lsp"},
		Stdout:      &out,
		Stderr:      &errOut,
		OpenBrowser: func(u string) {
			opened <- u
			cancel()
		},
	}
	if err := runRemote(ctx, remoteTarget{Dest: "me@vm", Path: "~/repo"}, opts); err != nil {
		t.Fatalf("runRemote: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}

	var local string
	select {
	case local = <-opened:
	default:
		t.Fatal("browser was never opened")
	}
	u, err := url.Parse(local)
	if err != nil || u.Hostname() != "127.0.0.1" || u.RawQuery != "path=main.go" {
		t.Fatalf("browser opened on %q", local)
	}

	log, _ := os.ReadFile(logPath)
	args := strings.TrimSpace(string(log))
	fwd := "-L 127.0.0.1:" + u.Port() + ":127.0.0.1:"
	if !strings.Contains(args, fwd) || !strings.Contains(args, "-o ExitOnForwardFailure=yes") || !strings.Contains(args, " -- me@vm sh -c ") {
		t.Errorf("unexpected ssh arguments: %s", args)
	}
	rest := args[strings.Index(args, fwd)+len(fwd):]
	rport := rest[:strings.IndexAny(rest, " ")]
	if _, err := strconv.Atoi(rport); err != nil || !strings.Contains(args, "-port "+rport+" -no-lsp -- \"$HOME\"/repo") {
		t.Errorf("remote px0 not started on forwarded port %s with passthrough flags: %s", rport, args)
	}

	got := out.String()
	for _, want := range []string{"remote:", "me@vm", "px0 9.9.9", "(local " + version + ")", "workspace:  /srv/app", "url:", local, "[OK] indexed 3 files"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "127.0.0.1:"+rport) {
		t.Errorf("remote url leaked into local output:\n%s", got)
	}
}

func TestRunRemoteInstallsLocalVersionWhenMissing(t *testing.T) {
	logPath, received := fakeSSH(t, "FreeBSD riscv64")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var out, errOut syncBuffer
	asked, fetched := "", ""
	opts := remoteOptions{
		Stdout: &out,
		Stderr: &errOut,
		Ask: func(q string) bool {
			asked = q
			return true
		},
		FetchAsset: func(goos, goarch string) (io.ReadCloser, error) {
			fetched = goos + "/" + goarch
			return io.NopCloser(strings.NewReader("FAKE BINARY")), nil
		},
		OpenBrowser: func(string) { cancel() },
	}
	if err := runRemote(ctx, remoteTarget{Dest: "vm"}, opts); err != nil {
		t.Fatalf("runRemote: %v\n%s\n%s", err, out.String(), errOut.String())
	}
	if !strings.Contains(asked, "vm (freebsd/riscv64)") || !strings.Contains(asked, version) {
		t.Errorf("install prompt = %q", asked)
	}
	if fetched != "freebsd/riscv64" {
		t.Errorf("fetched asset for %q", fetched)
	}
	if b, _ := os.ReadFile(received); string(b) != "FAKE BINARY" {
		t.Errorf("remote received %q", b)
	}
	log, _ := os.ReadFile(logPath)
	runs := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(runs) != 3 || !strings.Contains(runs[1], ".px0.tmp") || strings.Contains(runs[2], ".px0.tmp") {
		t.Fatalf("expected probe, copy and start sessions:\n%s", log)
	}
}

func TestRunRemoteCopiesOwnBinaryOnSamePlatform(t *testing.T) {
	sys := map[string]string{"linux": "Linux", "darwin": "Darwin", "freebsd": "FreeBSD"}[runtime.GOOS]
	machine := map[string]string{"amd64": "x86_64", "arm64": "arm64"}[runtime.GOARCH]
	if sys == "" || machine == "" {
		t.Skip("no uname spelling for this platform")
	}
	_, received := fakeSSH(t, sys+" "+machine)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	var out, errOut syncBuffer
	opts := remoteOptions{
		Stdout: &out,
		Stderr: &errOut,
		Ask:    func(string) bool { return true },
		FetchAsset: func(string, string) (io.ReadCloser, error) {
			t.Fatal("same platform must not download")
			return nil, nil
		},
		OpenBrowser: func(string) { cancel() },
	}
	if err := runRemote(ctx, remoteTarget{Dest: "vm"}, opts); err != nil {
		t.Fatalf("runRemote: %v\n%s\n%s", err, out.String(), errOut.String())
	}
	exe, _ := os.Executable()
	want, _ := os.Stat(exe)
	got, err := os.Stat(received)
	if err != nil || got.Size() != want.Size() {
		t.Errorf("remote did not receive the running binary (%v)", err)
	}
}

func TestRunRemoteRefusesWithoutInstallConsent(t *testing.T) {
	fakeSSH(t, "Linux x86_64")
	var out, errOut syncBuffer
	err := runRemote(context.Background(), remoteTarget{Dest: "vm"}, remoteOptions{
		Stdout: &out,
		Stderr: &errOut,
		Ask:    func(string) bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("expected a not-installed error, got %v", err)
	}
}

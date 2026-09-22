package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type remoteTarget struct {
	Host string
	Path string
}

type remoteFile struct {
	Path    string
	Name    string
	Size    int64
	ModTime int64
	Dir     bool
}

type remoteClient struct {
	target remoteTarget
	bin    string
}

func parseRemoteTarget(target string) (remoteTarget, bool) {
	if target == "" {
		return remoteTarget{}, false
	}
	if strings.HasPrefix(target, "ssh://") {
		rest := strings.TrimPrefix(target, "ssh://")
		slash := strings.IndexByte(rest, '/')
		if slash <= 0 {
			return remoteTarget{}, false
		}
		return remoteTarget{Host: rest[:slash], Path: cleanRemotePath(rest[slash:])}, true
	}
	colon := strings.IndexByte(target, ':')
	if colon <= 0 {
		return remoteTarget{}, false
	}
	if runtime.GOOS == "windows" && colon == 1 {
		return remoteTarget{}, false
	}
	host, path := target[:colon], target[colon+1:]
	if host == "" || path == "" || strings.ContainsAny(host, `/\`) {
		return remoteTarget{}, false
	}
	return remoteTarget{Host: host, Path: cleanRemotePath(path)}, true
}

func cleanRemotePath(raw string) string {
	if raw == "" {
		return "."
	}
	clean := path.Clean(raw)
	if clean == "." {
		return "."
	}
	return clean
}

func newRemoteClient(t remoteTarget) *remoteClient {
	bin := os.Getenv("PX0_SSH_BIN")
	if bin == "" {
		bin = "ssh"
	}
	return &remoteClient{target: t, bin: bin}
}

func (c *remoteClient) displayRoot() string {
	return c.target.Host + ":" + c.target.Path
}

func remoteRel(rel string) (string, bool) {
	if rel == "" {
		return "", true
	}
	if strings.HasPrefix(rel, "/") {
		return "", false
	}
	clean := path.Clean(rel)
	if clean == "." {
		return "", true
	}
	if clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", false
	}
	return clean, true
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (c *remoteClient) run(ctx context.Context, script string) ([]byte, error) {
	return c.runLimited(ctx, script, 0)
}

func (c *remoteClient) runLimited(ctx context.Context, script string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, c.target.Host, script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var out []byte
	if limit > 0 {
		out, err = io.ReadAll(io.LimitReader(stdout, limit+1))
	} else {
		out, err = io.ReadAll(stdout)
	}
	tooLarge := limit > 0 && int64(len(out)) > limit
	if tooLarge && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	if tooLarge {
		return nil, fmt.Errorf("file too large (%d bytes)", limit+1)
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) && stderr.Len() > 0 {
			return nil, fmt.Errorf("%w: %s", waitErr, strings.TrimSpace(stderr.String()))
		}
		return nil, waitErr
	}
	return out, nil
}

func (c *remoteClient) listFiles(ctx context.Context) ([]remoteFile, error) {
	root := shQuote(c.target.Path)
	out, err := c.run(ctx, "cd -- "+root+" && find . \\( -name .git -o -name .hg -o -name .svn \\) -prune -o -type d -exec sh -c 'for p do rel=${p#./}; [ \"$rel\" = . ] && continue; mtime=$(stat -c %Y \"$p\" 2>/dev/null || stat -f %m \"$p\" 2>/dev/null || echo 0); printf \"D\\000%s\\0000\\000%s\\000\" \"$rel\" \"$mtime\"; done' sh {} + -o -type f -exec sh -c 'for p do rel=${p#./}; size=$(wc -c < \"$p\") || exit 1; size=${size##*[!0-9]}; mtime=$(stat -c %Y \"$p\" 2>/dev/null || stat -f %m \"$p\" 2>/dev/null || echo 0); printf \"F\\000%s\\000%s\\000%s\\000\" \"$rel\" \"$size\" \"$mtime\"; done' sh {} +")
	if err != nil {
		return nil, err
	}
	var files []remoteFile
	parts := strings.Split(string(out), "\x00")
	for i := 0; i+3 < len(parts); i += 4 {
		kind, rawRel := parts[i], parts[i+1]
		if kind == "" && rawRel == "" {
			continue
		}
		rel, ok := remoteRel(rawRel)
		if !ok || rel == "" {
			continue
		}
		size, _ := strconv.ParseInt(parts[i+2], 10, 64)
		modFloat, _ := strconv.ParseFloat(parts[i+3], 64)
		name := rel[strings.LastIndexByte(rel, '/')+1:]
		switch kind {
		case "D":
			files = append(files, remoteFile{Path: rel, Name: name, ModTime: int64(modFloat * 1e9), Dir: true})
		case "F":
			files = append(files, remoteFile{Path: rel, Name: name, Size: size, ModTime: int64(modFloat * 1e9)})
		}
	}
	return files, nil
}

func (c *remoteClient) readFileLimit(ctx context.Context, rel string, limit int64) ([]byte, error) {
	script, err := c.readFileScript(rel, limit)
	if err != nil {
		return nil, err
	}
	return c.runLimited(ctx, script, limit)
}

func (c *remoteClient) spoolFile(ctx context.Context, rel string) (*os.File, func(), error) {
	script, err := c.readFileScript(rel, 0)
	if err != nil {
		return nil, func() {}, err
	}
	f, err := os.CreateTemp("", "px0-remote-raw-*")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() {
		name := f.Name()
		_ = f.Close()
		_ = os.Remove(name)
	}
	if err := c.runWrite(ctx, script, f); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return f, cleanup, nil
}

func (c *remoteClient) readFileScript(rel string, limit int64) (string, error) {
	rel, ok := remoteRel(rel)
	if !ok || rel == "" {
		return "", fmt.Errorf("bad path")
	}
	check := "rel=" + shQuote(rel) + "; root=$(pwd -P) || exit 1; path=$root; oldIFS=$IFS; set -f; IFS=/; set -- $rel; IFS=$oldIFS; for part do case $part in ''|.|..) exit 64;; esac; next=$path/$part; if [ -L \"$next\" ]; then exit 65; fi; path=$next; done"
	sizeCheck := ""
	if limit > 0 {
		sizeCheck = "; size=$(wc -c < \"$rel\") || exit 1; size=${size##*[!0-9]}; if [ \"$size\" -gt " + strconv.FormatInt(limit, 10) + " ]; then exit 66; fi"
	}
	return "cd -- " + shQuote(c.target.Path) + " && { " + check + sizeCheck + "; cat -- \"$rel\"; }", nil
}

func (c *remoteClient) runWrite(ctx context.Context, script string, dst io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, c.target.Host, script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, stdout)
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if copyErr != nil {
		return copyErr
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) && stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", waitErr, strings.TrimSpace(stderr.String()))
		}
		return waitErr
	}
	return nil
}

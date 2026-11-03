package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout bounds a run when Options.Timeout is zero.
	DefaultTimeout = 10 * time.Minute
	// DefaultLogBytes is how much of each output stream Result keeps.
	DefaultLogBytes = 32 << 10
)

// ErrCancelled is Result.Err when the caller's context was cancelled.
var ErrCancelled = errors.New("cancelled")

// Options configures Run. The zero value is valid.
type Options struct {
	Dir      string        // working directory for the harness
	Timeout  time.Duration // zero means DefaultTimeout
	LogBytes int           // output tail kept per stream; zero means DefaultLogBytes
	Stdout   io.Writer     // optional: receives the harness stdout as it is produced
	Stderr   io.Writer     // optional: receives the harness stderr as it is produced
	Env      []string      // extra KEY=VALUE entries added to the environment
}

// Result is the outcome of one Run.
type Result struct {
	Err      error         // nil on success; ErrCancelled, a timeout error, or the exec error
	Stdout   string        // the last LogBytes of stdout
	Stderr   string        // the last LogBytes of stderr
	Duration time.Duration // wall-clock time of the run
}

// Run executes cmd headless with prompt and blocks until the harness exits,
// ctx is cancelled, or the timeout elapses.
//
// On cancel or timeout the whole process group is killed, so helpers the
// harness started do not outlive it. stdin is left empty: a harness that
// still wants to ask a question fails fast instead of hanging.
//
// Run never returns a Go error. Everything, including failure to start,
// is reported in Result.Err.
func Run(ctx context.Context, cmd Command, prompt string, opt Options) Result {
	if len(cmd.Args) == 0 {
		return Result{Err: errors.New("no harness command")}
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	logBytes := opt.LogBytes
	if logBytes <= 0 {
		logBytes = DefaultLogBytes
	}

	// Fill the prompt into the argv. It is passed as one argument, never
	// through a shell, so it needs no quoting.
	args := make([]string, len(cmd.Args))
	for i, tok := range cmd.Args {
		args[i] = strings.ReplaceAll(tok, Placeholder, prompt)
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdout := &tailBuffer{max: logBytes}
	stderr := &tailBuffer{max: logBytes}
	c := exec.CommandContext(runCtx, args[0], args[1:]...)
	c.Dir = opt.Dir
	if len(opt.Env) > 0 {
		c.Env = append(c.Environ(), opt.Env...)
	}
	c.Stdout = tee(stdout, opt.Stdout)
	c.Stderr = tee(stderr, opt.Stderr)
	c.WaitDelay = 2 * time.Second // do not wait forever on output pipes held open by children
	setProcessGroup(c)

	start := time.Now()
	err := c.Run()

	// If the context ended, that is the real reason the run stopped, whatever
	// error the killed process reported. Tell cancel and timeout apart by
	// asking whether the caller's own context is the one that ended.
	if runCtx.Err() != nil {
		if ctx.Err() != nil {
			err = ErrCancelled
		} else {
			err = fmt.Errorf("gave up after %s", timeout)
		}
	}

	res := Result{
		Err:      err,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}
	return res
}

// tee sends output to buf and, when w is set, to w as well.
func tee(buf *tailBuffer, w io.Writer) io.Writer {
	if w == nil {
		return buf
	}
	return io.MultiWriter(buf, &lockedWriter{w: w})
}

// lockedWriter serialises writes. exec copies stdout and stderr from separate
// goroutines, so a caller who passes the same writer for both (or any writer
// that is not goroutine safe) would otherwise race.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// tailBuffer keeps only the last max bytes written to it: enough output to
// explain a failure without holding a whole session log in memory.
type tailBuffer struct {
	mu  sync.Mutex
	b   []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = append([]byte(nil), t.b[len(t.b)-t.max:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

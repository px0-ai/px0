package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

// Ape-style 256-color palette
const (
	colorAccent = "208" // amber
	colorOk     = "71"  // muted green
	colorErr    = "167" // muted red
	colorWarn   = "179" // muted amber-yellow
	colorInfo   = "110" // muted blue
	colorDim    = "245" // dim grey
	colorFaint  = "240" // faint grey
)

var (
	uiForcedColor *bool
	uiQuiet       = false
	uiVerbose     = false
)

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func uiVerbosePrompt(jobID int64, harness string, prompt string, w io.Writer) {
	if !uiVerbose || uiQuiet {
		return
	}
	lines := strings.Split(prompt, "\n")
	fmt.Fprintf(w, "  %s %s\n", uiDim("prompt:", w), uiFaint(fmt.Sprintf("(%d lines)", len(lines)), w))
	for _, l := range lines {
		fmt.Fprintf(w, "  %s %s\n", uiFaint("│", w), uiDim(l, w))
	}
}

func colorEnabled(w io.Writer) bool {
	if uiForcedColor != nil {
		return *uiForcedColor
	}
	if v, exists := os.LookupEnv("NO_COLOR"); exists && v != "" {
		return false
	}
	if v, exists := os.LookupEnv("FORCE_COLOR"); exists {
		return v != "0" && v != "false"
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTTY(w)
}

func paint(text string, code string, bold bool, w io.Writer) string {
	if text == "" || !colorEnabled(w) {
		return text
	}
	prefix := ""
	if bold {
		prefix = "\033[1m"
	}
	return fmt.Sprintf("%s\033[38;5;%sm%s\033[0m", prefix, code, text)
}

func uiDim(t string, w io.Writer) string    { return paint(t, colorDim, false, w) }
func uiFaint(t string, w io.Writer) string  { return paint(t, colorFaint, false, w) }
func uiAccent(t string, w io.Writer) string { return paint(t, colorAccent, false, w) }
func uiInfo(t string, w io.Writer) string   { return paint(t, colorInfo, false, w) }
func uiStrong(t string, w io.Writer) string {
	if colorEnabled(w) {
		return "\033[1m" + t + "\033[0m"
	}
	return t
}

func uiGlyph(role string, w io.Writer) string {
	tty := isTTY(w)
	var mark, fallback, code string
	switch role {
	case "ok":
		mark, fallback, code = "✓", "[OK]", colorOk
	case "err":
		mark, fallback, code = "✗", "[FAIL]", colorErr
	case "warn":
		mark, fallback, code = "!", "[WARN]", colorWarn
	case "info":
		mark, fallback, code = "·", "[INFO]", colorInfo
	case "step":
		mark, fallback, code = "›", ">", colorAccent
	default:
		mark, fallback, code = "·", "[INFO]", colorInfo
	}
	s := mark
	if !tty {
		s = fallback
	}
	return paint(s, code, false, w)
}

func uiStatus(role, msg, detail string, width int, w io.Writer) {
	if uiQuiet {
		return
	}
	padded := msg
	if width > 0 && len(msg) < width {
		padded = msg + strings.Repeat(" ", width-len(msg))
	}
	line := fmt.Sprintf("%s %s", uiGlyph(role, w), padded)
	if detail != "" {
		line += fmt.Sprintf("  %s", uiDim(detail, w))
	}
	fmt.Fprintln(w, line)
}

func uiHeading(title string, count *int, w io.Writer) {
	if uiQuiet {
		return
	}
	fmt.Fprintln(w)
	line := uiStrong(title, w)
	if count != nil {
		line += fmt.Sprintf(" %s", uiDim(fmt.Sprintf("(%d)", *count), w))
	}
	fmt.Fprintln(w, line)
}

func uiKV(label, value string, width int, w io.Writer) {
	if uiQuiet {
		return
	}
	labelText := label + ":"
	if width > 0 && len(labelText) < width {
		labelText = labelText + strings.Repeat(" ", width-len(labelText))
	}
	fmt.Fprintf(w, "  %s %s\n", uiDim(labelText, w), value)
}

func uiHint(text string, w io.Writer) {
	if uiQuiet {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, uiDim(text, w))
}

func uiCommand(text string, w io.Writer) {
	if uiQuiet {
		return
	}
	fmt.Fprintf(w, "  %s\n", uiAccent(text, w))
}

func uiBullet(text string, w io.Writer) {
	if uiQuiet {
		return
	}
	fmt.Fprintf(w, "  %s %s\n", uiFaint("·", w), text)
}

// uiSpinner displays an animated spinner on TTY terminals, or clean step messages on non-TTYs.
type uiSpinner struct {
	w       io.Writer
	msg     string
	mu      sync.Mutex
	done    chan struct{}
	stopped bool
	tty     bool
	start   time.Time
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func newSpinner(msg string, w io.Writer) *uiSpinner {
	s := &uiSpinner{
		w:     w,
		msg:   msg,
		done:  make(chan struct{}),
		tty:   isTTY(w) && !uiQuiet,
		start: time.Now(),
	}
	if uiQuiet {
		s.stopped = true
		return s
	}
	if !s.tty {
		fmt.Fprintf(s.w, "%s %s\n", uiGlyph("step", s.w), msg)
		return s
	}

	go s.run()
	return s
}

func (s *uiSpinner) run() {
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	idx := 0
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.stopped {
				s.mu.Unlock()
				return
			}
			frame := paint(spinnerFrames[idx%len(spinnerFrames)], colorAccent, true, s.w)
			fmt.Fprintf(s.w, "\r\033[K%s %s", frame, s.msg)
			idx++
			s.mu.Unlock()
		}
	}
}

func (s *uiSpinner) Update(msg string) {
	if uiQuiet {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msg = msg
	if !s.tty {
		fmt.Fprintf(s.w, "%s %s\n", uiGlyph("step", s.w), msg)
	}
}

func (s *uiSpinner) Stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	close(s.done)
	if s.tty {
		fmt.Fprint(s.w, "\r\033[K")
	}
	s.mu.Unlock()
}

func (s *uiSpinner) Success(msg string) {
	s.Stop()
	if uiQuiet {
		return
	}
	elapsed := fmtDuration(time.Since(s.start))
	uiStatus("ok", msg, elapsed, 0, s.w)
}

func (s *uiSpinner) Fail(msg string) {
	s.Stop()
	if uiQuiet {
		return
	}
	uiStatus("err", msg, "", 0, s.w)
}


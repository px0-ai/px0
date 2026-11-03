package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Harness is one row of a picker: a known harness, the model it would use, and
// whether it is installed. Name and Model are exactly what Resolve takes:
//
//	cmd, err := harness.Resolve(h.Name, h.Model)
type Harness struct {
	Name      string `json:"name"`            // harness name, e.g. "claude"; pass to Resolve
	Cmd       string `json:"cmd"`             // the command line that would run, with {prompt} unfilled
	Installed bool   `json:"installed"`       // the binary was found
	Path      string `json:"path,omitempty"`  // absolute path of the binary, when installed
	Model     string `json:"model,omitempty"` // model you asked for; "" means the harness picks its own. Pass to Resolve
}

// Detect reports every known harness and whether it is installed right now.
//
// It checks the machine on every call, so a tool installed after startup
// shows up without a restart. models maps a harness name to the model the
// caller wants for it (nil is fine). A harness not in the map has no model
// chosen, so it uses its own. Detect does not run any harness.
func Detect(models map[string]string) []Harness {
	out := make([]Harness, 0, len(presets))
	for _, p := range Presets() {
		bin, installed := LookPath(p.Args[0])

		model := models[p.Name] // "" when the caller chose none
		// Show the real command line when it resolves, else the bare template.
		cmd := strings.Join(p.Args, " ")
		if c, err := Resolve(p.Name, model); err == nil {
			cmd = strings.Join(c.Args, " ")
		}

		out = append(out, Harness{
			Name:      p.Name,
			Cmd:       cmd,
			Installed: installed,
			Path:      bin,
			Model:     model,
		})
	}
	return out
}

// Installed lists the names of the known harnesses found on this machine.
func Installed() []string {
	var names []string
	for _, h := range Detect(nil) {
		if h.Installed {
			names = append(names, h.Name)
		}
	}
	return names
}

// BinDirs lists folders installers put binaries in that are often missing
// from PATH, so a harness installed by hand after the process started is found
// without restarting the shell it was launched from.
func BinDirs() []string {
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
		add(home, "go", "bin")
		add(home, ".cargo", "bin")
		add(home, ".local", "bin")
		add(home, ".opencode", "bin")
	}
	// npm puts global packages beside its own executable unless the prefix was moved.
	if npm, err := exec.LookPath("npm"); err == nil {
		add(filepath.Dir(npm))
	}
	switch runtime.GOOS {
	case "darwin":
		add("/opt/homebrew/bin")
		add("/usr/local/bin")
	case "windows":
		if v := os.Getenv("APPDATA"); v != "" {
			add(v, "npm")
		}
	}
	return dirs
}

// LookPath finds a command on PATH, then in BinDirs. On Windows exec.LookPath
// also tries PATHEXT extensions for a joined path, so "npm" finds npm.cmd.
func LookPath(name string) (string, bool) {
	return lookPathIn(name, BinDirs())
}

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

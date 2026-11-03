package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

// listTimeout bounds the harness's own "list models" command.
const listTimeout = 5 * time.Second

// lister describes how to ask one harness for its models: the arguments to
// run, and how to turn the printed output into a list of model names.
// Every preset has an entry (a test enforces it).
type lister struct {
	args  []string
	parse func(output string) []string
}

var listers = map[string]lister{
	"claude":       {[]string{"-p", "/model"}, parseClaudeModels},
	"cursor-agent": {[]string{"--list-models"}, parseCursorModels},
	"agy":          {[]string{"models"}, parseAgyModels},
	"opencode":     {[]string{"models"}, parseOpencodeModels},
	"codex":        {[]string{"debug", "models"}, parseCodexModels},
}

// Successful lookups are cached for the life of the process, because asking a
// harness for its models can take seconds.
var (
	modelsMu    sync.Mutex
	modelsCache = map[string][]string{}
)

// DiscoverModels runs the installed harness's own command to list the models
// it offers, in the order the harness reports them.
//
// It returns an error when the name is not a known harness, the harness is not
// installed, or its list command fails. There is no fallback list: what you
// get is what the harness says.
func DiscoverModels(ctx context.Context, name string) ([]string, error) {
	p, ok := LookupPreset(name)
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", name)
	}
	l, ok := listers[p.Name]
	if !ok {
		return nil, fmt.Errorf("%s has no model list command (bug: add it to listers)", p.Name)
	}

	modelsMu.Lock()
	cached, ok := modelsCache[p.Name]
	modelsMu.Unlock()
	if ok {
		return slices.Clone(cached), nil
	}

	bin, ok := LookPath(p.Args[0])
	if !ok {
		return nil, fmt.Errorf("%s is not installed", p.Args[0])
	}

	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, l.args...)
	cmd.Stdin = strings.NewReader("") // never wait for input
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: listing models: %w", p.Name, err)
	}
	list := l.parse(string(out))
	if len(list) == 0 {
		return nil, fmt.Errorf("%s: no models found in its output", p.Name)
	}

	modelsMu.Lock()
	modelsCache[p.Name] = list
	modelsMu.Unlock()
	return slices.Clone(list), nil
}

// lines returns the trimmed, non-empty lines of s.
func lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out
}

// parseClaudeModels reads the "Available: a, b, or c." line that
// `claude -p /model` prints.
func parseClaudeModels(s string) []string {
	_, avail, ok := strings.Cut(s, "Available:")
	if !ok {
		return nil
	}
	avail, _, _ = strings.Cut(avail, ".") // the list ends at the first period
	var list []string
	for _, part := range strings.Split(avail, ",") {
		m := strings.TrimPrefix(strings.TrimSpace(part), "or ")
		if m != "" && !strings.Contains(m, " ") {
			list = append(list, m)
		}
	}
	return list
}

// parseCursorModels reads lines of the form "<id> - <description>", skipping
// the "Tip:" hint line.
func parseCursorModels(s string) []string {
	var list []string
	for _, line := range lines(s) {
		if strings.HasPrefix(line, "Tip:") {
			continue
		}
		id, _, _ := strings.Cut(line, " - ")
		if id = strings.TrimSpace(id); id != "" && !strings.Contains(id, " ") {
			list = append(list, id)
		}
	}
	return list
}

// parseAgyModels takes the first word of each line, skipping the
// "Fetching ..." progress line.
func parseAgyModels(s string) []string {
	var list []string
	for _, line := range lines(s) {
		if !strings.HasPrefix(line, "Fetching") {
			list = append(list, strings.Fields(line)[0])
		}
	}
	return list
}

// parseOpencodeModels keeps lines that are a single word, such as
// "provider/model", and drops any prose around them.
func parseOpencodeModels(s string) []string {
	var list []string
	for _, line := range lines(s) {
		if !strings.Contains(line, " ") {
			list = append(list, line)
		}
	}
	return list
}

// parseCodexModels reads the catalog `codex debug models` prints as JSON and
// keeps the models Codex itself lists in its picker (visibility "list"), in
// catalog order. Anything printed before the JSON object is ignored.
func parseCodexModels(s string) []string {
	start := strings.Index(s, "{")
	if start < 0 {
		return nil
	}
	var doc struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.NewDecoder(strings.NewReader(s[start:])).Decode(&doc); err != nil {
		return nil
	}
	var list []string
	for _, m := range doc.Models {
		if m.Slug != "" && (m.Visibility == "" || m.Visibility == "list") {
			list = append(list, m.Slug)
		}
	}
	return list
}

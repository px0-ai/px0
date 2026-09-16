package main

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"unicode/utf16"
)

type lspOpenDocument struct {
	version int
	modTime int64
	size    int64
}

type lspDiagnostic struct {
	Range    lspRange        `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
}

type lspDiagnosticSnapshot struct {
	version int
	items   []lspDiagnostic
}

// Diagnostic is one LSP problem in browser line and column coordinates.
type Diagnostic struct {
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"endLine"`
	EndCol   int    `json:"endCol"`
	Severity int    `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
	Code     string `json:"code,omitempty"`
}

func (c *lspClient) onDiagnostics(raw json.RawMessage) {
	var p struct {
		URI         string          `json:"uri"`
		Version     *int            `json:"version"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}
	if json.Unmarshal(raw, &p) != nil || p.URI == "" {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	doc, ok := c.opened[p.URI]
	if !ok || (p.Version != nil && *p.Version != doc.version) {
		return
	}
	items := append([]lspDiagnostic(nil), p.Diagnostics...)
	if items == nil {
		items = []lspDiagnostic{}
	}
	c.diagnostics[p.URI] = lspDiagnosticSnapshot{version: doc.version, items: items}
}

func (c *lspClient) diagnosticSnapshot(abs string) (lspDiagnosticSnapshot, bool) {
	uri := pathToURI(abs)
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot, ok := c.diagnostics[uri]
	doc, open := c.opened[uri]
	if !ok || !open || snapshot.version != doc.version {
		return lspDiagnosticSnapshot{}, false
	}
	snapshot.items = append([]lspDiagnostic(nil), snapshot.items...)
	return snapshot, true
}

func byteToUTF16(s string, byteCol int) int {
	if byteCol <= 0 {
		return 0
	}
	if byteCol > len(s) {
		byteCol = len(s)
	}
	units := 0
	for at, r := range s {
		if at >= byteCol {
			break
		}
		units += len(utf16.Encode([]rune{r}))
	}
	return units
}

func diagnosticCode(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	return strings.TrimSpace(string(raw))
}

// Diagnostics returns the latest complete publication for one open document.
func (m *lspManager) Diagnostics(ctx context.Context, abs, rel string) ([]Diagnostic, bool, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, false, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, false, err
	}

	snapshot, seen := c.diagnosticSnapshot(abs)
	if !seen {
		return []Diagnostic{}, true, nil
	}
	if len(snapshot.items) == 0 {
		return []Diagnostic{}, false, nil
	}

	d, err := Open(abs, rel)
	if err != nil {
		return nil, false, err
	}
	lines := d.RawLines()
	position := func(p lspPosition) (int, int) {
		line, byteCol := c.fromLSP(lines, p)
		if line < 1 {
			line = 1
		}
		if line > len(lines) {
			line = len(lines)
		}
		if line < 1 {
			return 1, 0
		}
		return line, byteToUTF16(lines[line-1], byteCol)
	}

	out := make([]Diagnostic, 0, len(snapshot.items))
	for _, item := range snapshot.items {
		line, col := position(item.Range.Start)
		endLine, endCol := position(item.Range.End)
		if endLine < line || endLine == line && endCol < col {
			endLine, endCol = line, col
		}
		out = append(out, Diagnostic{
			Line: line, Col: col, EndLine: endLine, EndCol: endCol,
			Severity: item.Severity, Message: item.Message,
			Source: item.Source, Code: diagnosticCode(item.Code),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		if out[i].Col != out[j].Col {
			return out[i].Col < out[j].Col
		}
		return out[i].Severity < out[j].Severity
	})
	return out, false, nil
}

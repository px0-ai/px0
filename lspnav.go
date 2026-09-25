package main

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// NavHit is one navigation result, shaped exactly like a search Match so the
// UI renders LSP answers and regex answers with the same code.
type NavHit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Pre  string `json:"pre"`
	Mid  string `json:"mid"`
	Post string `json:"post"`
	Def  bool   `json:"def,omitempty"`
	Ext  bool   `json:"ext,omitempty"` // outside the indexed tree, e.g. stdlib
}

// utf16ToByte converts a column measured in UTF-16 code units (what JavaScript
// string indexes count) into a byte offset into the same line.
func utf16ToByte(line string, u16 int) int {
	if u16 <= 0 {
		return 0
	}
	units, bytes := 0, 0
	for _, r := range line {
		if units >= u16 {
			break
		}
		units += len(utf16.Encode([]rune{r}))
		bytes += utf8.RuneLen(r)
	}
	if bytes > len(line) {
		return len(line)
	}
	return bytes
}

// resolve turns a set of LSP ranges into display-ready hits, reading each target
// file through the same cache the editor uses.
func (m *lspManager) resolve(c *lspClient, locs []lspLocation, markDef bool) []NavHit {
	type fileKey struct {
		abs, rel string
		ext      bool
	}
	byFile := map[fileKey][]lspRange{}
	var order []fileKey

	for _, l := range locs {
		abs, err := uriToPath(l.URI)
		if err != nil {
			continue
		}
		// Outside the indexed tree is still worth jumping to, so treePath
		// allows this exact file to be opened even though the tree guard
		// would refuse it.
		rel, ext := m.treePath(abs, true)
		k := fileKey{abs, rel, ext}
		if _, seen := byFile[k]; !seen {
			order = append(order, k)
		}
		byFile[k] = append(byFile[k], l.Range)
	}

	var out []NavHit
	for _, k := range order {
		d, err := Open(k.abs, k.rel)
		var lines []string
		if err == nil {
			lines = d.RawLines()
		}
		ranges := byFile[k]
		sort.Slice(ranges, func(i, j int) bool {
			if ranges[i].Start.Line != ranges[j].Start.Line {
				return ranges[i].Start.Line < ranges[j].Start.Line
			}
			return ranges[i].Start.Character < ranges[j].Start.Character
		})
		seen := map[int]bool{}
		for _, r := range ranges {
			line, from := c.fromLSP(lines, r.Start)
			if seen[line] {
				continue // one entry per line keeps the list readable
			}
			seen[line] = true

			text := ""
			if line-1 >= 0 && line-1 < len(lines) {
				text = lines[line-1]
			}
			to := from
			if r.End.Line == r.Start.Line {
				_, to = c.fromLSP(lines, r.End)
			}
			if to <= from || to > len(text) {
				to = len(text)
				if from > to {
					from = to
				}
			}
			h := snip([]byte(text), from, to)
			out = append(out, NavHit{
				Path: k.rel, Line: line, Pre: h.Pre, Mid: h.Mid, Post: h.Post,
				Def: markDef, Ext: k.ext,
			})
		}
	}
	return out
}

// locate runs one position-based request and normalises the two shapes a server
// may answer with (Location or LocationLink).
func (m *lspManager) locate(ctx context.Context, method, abs, rel string, line, u16col int, extra map[string]any) ([]NavHit, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}

	d, err := Open(abs, rel)
	if err != nil {
		return nil, err
	}
	byteCol := utf16ToByte(d.Raw(line), u16col)

	params := map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     c.toLSP(d.Raw(line), line, byteCol),
	}
	for k, v := range extra {
		params[k] = v
	}

	var raw []map[string]any
	if err := c.call(ctx, method, params, &raw); err != nil {
		// A single Location, not an array, is also legal.
		var one lspLocation
		if err2 := c.call(ctx, method, params, &one); err2 != nil || one.URI == "" {
			return nil, err
		}
		return m.resolve(c, []lspLocation{one}, method != "textDocument/references"), nil
	}

	locs := make([]lspLocation, 0, len(raw))
	for _, item := range raw {
		if uri, ok := item["uri"].(string); ok {
			locs = append(locs, lspLocation{URI: uri, Range: decodeRange(item["range"])})
			continue
		}
		if uri, ok := item["targetUri"].(string); ok {
			rng := item["targetSelectionRange"]
			if rng == nil {
				rng = item["targetRange"]
			}
			locs = append(locs, lspLocation{URI: uri, Range: decodeRange(rng)})
		}
	}
	return m.resolve(c, locs, method != "textDocument/references"), nil
}

func decodeRange(v any) lspRange {
	mv, ok := v.(map[string]any)
	if !ok {
		return lspRange{}
	}
	pos := func(key string) lspPosition {
		p, ok := mv[key].(map[string]any)
		if !ok {
			return lspPosition{}
		}
		l, _ := p["line"].(float64)
		c, _ := p["character"].(float64)
		return lspPosition{Line: int(l), Character: int(c)}
	}
	return lspRange{Start: pos("start"), End: pos("end")}
}

// Definition queries the language server for the definition site(s) of the symbol at the given position.
// Line is 1-based and col is the UTF-16 code unit offset from the frontend.
func (m *lspManager) Definition(ctx context.Context, abs, rel string, line, col int) ([]NavHit, error) {
	return m.locate(ctx, "textDocument/definition", abs, rel, line, col, nil)
}

// References queries the language server for all reference locations of the symbol at the given position,
// including its declaration site.
func (m *lspManager) References(ctx context.Context, abs, rel string, line, col int) ([]NavHit, error) {
	return m.locate(ctx, "textDocument/references", abs, rel, line, col,
		map[string]any{"context": map[string]any{"includeDeclaration": true}})
}

// Symbols returns the document outline, flattened with indentation that mirrors
// the server's nesting.
func (m *lspManager) Symbols(ctx context.Context, abs, rel string) ([]Symbol, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}

	var raw []lspDocumentSymbol
	if err := c.call(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
	}, &raw); err != nil {
		return nil, err
	}

	var out []Symbol
	var walk func(syms []lspDocumentSymbol, depth int)
	walk = func(syms []lspDocumentSymbol, depth int) {
		for _, s := range syms {
			rng := s.SelectionRange
			if s.Location != nil {
				rng = s.Location.Range // symbolInformation form
			}
			kind := lspSymbolKind[s.Kind]
			if kind == "" {
				kind = "sym"
			}
			out = append(out, Symbol{
				Name: s.Name, Kind: kind, Line: rng.Start.Line + 1, Indent: depth * 2,
			})
			if len(s.Children) > 0 {
				walk(s.Children, depth+1)
			}
		}
	}
	walk(raw, 0)

	// symbolInformation comes back unordered often enough to be worth fixing.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out, nil
}

// ---------------------------------------------------------------- hover

// HoverInfo is what the UI shows in the hover card: a syntax-highlighted
// signature and whatever documentation the server attached to it.
type HoverInfo struct {
	Signature string `json:"signature"` // pre-highlighted HTML, one line per entry
	Doc       string `json:"doc"`
	Empty     bool   `json:"empty"`
}

// Hover asks the server what a position is, and renders the answer.
func (m *lspManager) Hover(ctx context.Context, abs, rel string, line, col int) (*HoverInfo, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}
	d, err := Open(abs, rel)
	if err != nil {
		return nil, err
	}
	byteCol := utf16ToByte(d.Raw(line), col)

	var res struct {
		Contents json.RawMessage `json:"contents"`
	}
	err = c.call(ctx, "textDocument/hover", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     c.toLSP(d.Raw(line), line, byteCol),
	}, &res)
	if err != nil {
		return nil, err
	}
	if len(res.Contents) == 0 {
		return &HoverInfo{Empty: true}, nil
	}

	code, prose := parseHoverContents(res.Contents)
	if code == "" && prose == "" {
		return &HoverInfo{Empty: true}, nil
	}
	return &HoverInfo{Signature: highlightSnippet(code, rel), Doc: prose}, nil
}

// parseHoverContents flattens the three shapes the spec allows: a MarkupContent
// object, a MarkedString, or an array of MarkedStrings.
func parseHoverContents(raw json.RawMessage) (code, prose string) {
	var markup struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &markup); err == nil && markup.Value != "" {
		return splitMarkdown(markup.Value)
	}

	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return splitMarkdown(one)
	}

	var many []json.RawMessage
	if err := json.Unmarshal(raw, &many); err == nil {
		var codes, proses []string
		for _, item := range many {
			var s string
			if json.Unmarshal(item, &s) == nil {
				c, p := splitMarkdown(s)
				if c != "" {
					codes = append(codes, c)
				}
				if p != "" {
					proses = append(proses, p)
				}
				continue
			}
			var ms struct {
				Language string `json:"language"`
				Value    string `json:"value"`
			}
			if json.Unmarshal(item, &ms) == nil && ms.Value != "" {
				if ms.Language != "" {
					codes = append(codes, ms.Value)
				} else {
					proses = append(proses, ms.Value)
				}
			}
		}
		return strings.Join(codes, "\n"), strings.Join(proses, "\n\n")
	}
	return "", ""
}

// splitMarkdown pulls fenced code blocks out of a hover body; what is left is
// documentation prose.
func splitMarkdown(s string) (code, prose string) {
	var codes, text []string
	inFence := false
	var fence []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				codes = append(codes, strings.Join(fence, "\n"))
				fence = nil
			}
			inFence = !inFence
			continue
		}
		if inFence {
			fence = append(fence, line)
		} else {
			text = append(text, line)
		}
	}
	if len(fence) > 0 {
		codes = append(codes, strings.Join(fence, "\n"))
	}
	code = strings.TrimSpace(strings.Join(codes, "\n"))
	prose = strings.TrimSpace(stripMarkdown(strings.Join(text, "\n")))

	// A server that only speaks plaintext sends no fences at all. Its first
	// line is still the declaration, so promote it rather than showing nothing.
	if code == "" && prose != "" {
		first, rest, _ := strings.Cut(prose, "\n")
		if looksLikeDeclaration(first) {
			code, prose = first, strings.TrimSpace(rest)
		}
	}
	const maxProse = 900
	if len(prose) > maxProse {
		if cut := strings.LastIndex(prose[:maxProse], " "); cut > 0 {
			prose = prose[:cut]
		} else {
			prose = prose[:maxProse]
		}
		prose += "…"
	}
	return code, prose
}

// looksLikeDeclaration is a deliberately conservative test: short, no trailing
// sentence punctuation, and carrying syntax a prose line would not.
func looksLikeDeclaration(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 200 || strings.HasSuffix(s, ".") {
		return false
	}
	return strings.ContainsAny(s, "(){}[]<>:=*&") || strings.Contains(s, " ")
}

var (
	mdInline = strings.NewReplacer("**", "", "__", "", "`", "")
	mdLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// No backreference: Go's regexp is RE2, so the three rule forms are spelled out.
	mdRule     = regexp.MustCompile(`^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	mdOnlyLink = regexp.MustCompile(`^\s*\[[^\]]*\]\([^)]*\)\s*$`)
)

// stripMarkdown flattens a hover body into something readable in a tooltip.
// Nothing here tries to be a markdown renderer; it removes the decorations that
// would otherwise show up as literal punctuation.
func stripMarkdown(s string) string {
	var out []string
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		if mdRule.MatchString(line) || mdOnlyLink.MatchString(line) {
			continue // horizontal rules and bare "see also" links are noise
		}
		line = strings.TrimRight(mdInline.Replace(mdLink.ReplaceAllString(line, "$1")), " ")
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

const maxSignatureLines = 12

// highlightSnippet runs a hover signature through the same lexer the file uses,
// so the card matches the editor.
func highlightSnippet(code, rel string) string {
	if code == "" {
		return ""
	}
	lines := strings.Split(code, "\n")
	if len(lines) > maxSignatureLines {
		lines = append(lines[:maxSignatureLines], "…")
		code = strings.Join(lines, "\n")
	}
	d := newDoc(code, rel)
	out, _ := d.Lines(0, d.Total)
	return strings.Join(out, "\n")
}

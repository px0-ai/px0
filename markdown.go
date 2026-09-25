package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const (
	// A preview renders the whole file in one pass, unlike the windowed code
	// view, so it stops at a size that still converts in well under a second.
	maxMarkdownBytes = 4 << 20

	// Fenced blocks larger than this are shown without highlighting.
	maxFenceBytes = 256 << 10
)

func isMarkdown(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".md", ".markdown":
		return true
	}
	return false
}

// mdConverter renders GitHub Flavored Markdown. Raw HTML is passed through on
// purpose, because READMEs lean on it for centred logos and <details>. Nothing
// it produces is trusted: the browser rebuilds the result against an allowlist
// before it reaches the page (sanitize in web/src/markdown.js).
var mdConverter = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
		parser.WithASTTransformers(util.Prioritized(lineMarker{}, 100)),
	),
	goldmark.WithRendererOptions(
		html.WithUnsafe(),
		renderer.WithNodeRenderers(util.Prioritized(fenceRenderer{}, 100)),
	),
)

func hideFrontmatter(src []byte) {
	if !bytes.HasPrefix(src, []byte("---\n")) && !bytes.HasPrefix(src, []byte("+++\n")) {
		return
	}
	marker := src[:3]
	search := append([]byte("\n"), marker...)

	endIdx := -1
	offset := 3
	for {
		idx := bytes.Index(src[offset:], search)
		if idx == -1 {
			break
		}

		afterMarker := offset + idx + 4
		if afterMarker == len(src) || src[afterMarker] == '\n' {
			endIdx = offset + idx + 1
			break
		}

		offset += idx + 4
	}

	if endIdx == -1 {
		return
	}

	endOfFrontmatter := endIdx + 3
	if endOfFrontmatter < len(src) && src[endOfFrontmatter] == '\n' {
		endOfFrontmatter++
	}

	for i := 0; i < endOfFrontmatter; i++ {
		if src[i] != '\n' {
			src[i] = ' '
		}
	}
}

func renderMarkdown(src []byte) (string, error) {
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
	hideFrontmatter(src)
	var buf bytes.Buffer
	buf.Grow(len(src) * 2)
	ctx := parser.NewContext(parser.WithIDs(&headingIDs{seen: map[string]bool{}}))
	if err := mdConverter.Convert(src, &buf, parser.WithContext(ctx)); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// headingIDs makes anchors the way GitHub does, so a table of contents written
// for GitHub (#install, #api_reference, #überblick) works here too: lower case,
// letters, digits, - and _ kept, spaces turned into -, everything else dropped.
type headingIDs struct{ seen map[string]bool }

func (h *headingIDs) Generate(value []byte, kind ast.NodeKind) []byte {
	var b strings.Builder
	for _, r := range strings.TrimSpace(string(value)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-':
			b.WriteRune(unicode.ToLower(r))
		case r == ' ':
			b.WriteByte('-')
		}
	}
	id := b.String()
	if id == "" {
		id = "section"
	}
	if h.seen[id] {
		for i := 1; ; i++ {
			if c := id + "-" + strconv.Itoa(i); !h.seen[c] {
				id = c
				break
			}
		}
	}
	h.seen[id] = true
	return []byte(id)
}

func (h *headingIDs) Put(value []byte) { h.seen[string(value)] = true }

// lineMarker tags blocks with the 1-based source line they start on
// (data-line). The preview uses it to follow line-based navigation (outline,
// go to line, search hits, history) and to keep its place when the reader
// switches to the source view and back.
type lineMarker struct{}

func (lineMarker) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	var newlines []int
	for i, c := range reader.Source() {
		if c == '\n' {
			newlines = append(newlines, i)
		}
	}
	ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindHeading, ast.KindParagraph, ast.KindList, ast.KindListItem, ast.KindBlockquote,
			ast.KindFencedCodeBlock, ast.KindCodeBlock, extast.KindTable:
			if off, ok := blockStart(n); ok {
				n.SetAttributeString("data-line", strconv.Itoa(sort.SearchInts(newlines, off)+1))
			}
		}
		return ast.WalkContinue, nil
	})
}

// blockStart finds the first source byte of a block. Containers such as lists
// and blockquotes hold no lines of their own, so it descends to the first child
// that does.
func blockStart(n ast.Node) (int, bool) {
	for ; n != nil; n = n.FirstChild() {
		if n.Type() == ast.TypeBlock && n.Lines().Len() > 0 {
			return n.Lines().At(0).Start, true
		}
	}
	return 0, false
}

// fenceRenderer highlights code blocks with the lexers and token classes of the
// code view, so one theme colours both.
type fenceRenderer struct{}

func (fenceRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, renderFence)
	reg.Register(ast.KindCodeBlock, renderFence)
}

func renderFence(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(f.Language(src))
	}
	var code strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		code.Write(seg.Value(src))
	}
	w.WriteString(`<pre class="md-code"`)
	if v, ok := n.AttributeString("data-line"); ok {
		if s, ok := v.(string); ok {
			w.WriteString(` data-line="` + s + `"`)
		}
	}
	if lang != "" {
		w.WriteString(` data-lang="`)
		w.Write(util.EscapeHTML([]byte(lang)))
		w.WriteByte('"')
	}
	w.WriteString("><code>")
	w.WriteString(highlightFence(strings.TrimSuffix(code.String(), "\n"), lang))
	w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// highlightFence colours a block whose fence names a language chroma knows.
// Unlabelled blocks stay plain: guessing from content is slow and often wrong.
func highlightFence(code, lang string) string {
	var lexer chroma.Lexer
	if lang != "" && len(code) <= maxFenceBytes {
		lexer = lexers.Get(lang)
	}
	if lexer == nil {
		return htmlEscaper.Replace(code)
	}
	return strings.Join(highlightLines(chroma.Coalesce(lexer), code, strings.Count(code, "\n")+1), "\n")
}

// handleMarkdown serves a Markdown file rendered for the preview.
func (s *Server) handleMarkdown(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	if !isMarkdown(rel) {
		fail(w, 415, "not a Markdown file")
		return
	}
	st, err := os.Stat(abs)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if st.IsDir() {
		fail(w, 415, "is a directory")
		return
	}
	if st.Size() > maxMarkdownBytes {
		fail(w, 413, "too large to preview")
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	out, err := renderMarkdown(data)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"path": rel, "html": out})
}

package render

import (
	"bytes"
	"container/list"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	maxFileBytes = 64 << 20 // refuse to open anything larger

	// Chroma's lexers run at well under 1 MB/s, so tokenising a whole file on
	// open is off the table. We lex one window at a time instead: hlChunk lines
	// padded with hlContext throwaway lines on each side. The leading pad puts
	// the lexer in the right state on entry (inside a block comment, a raw
	// string); the trailing pad lets a construct that opens inside the window
	// find its terminator instead of being reported as unterminated.
	ChunkLines = 1000
	hlChunk    = ChunkLines
	hlContext  = 400

	// A window is measured in lines, but generated files exist with a handful
	// of multi-megabyte lines, where a 1000-line window is the whole file. Cap
	// the window in bytes too: drop the context first, then stop highlighting
	// altogether rather than hand chroma something it will choke on.
	hlWindowBytes = 512 << 10

	// The client sizes its horizontal scroll area from MaxCols. A line of a few
	// million characters would ask the browser for a surface it cannot make, so
	// the reported width stops here.
	maxReportedCols = 20000

	// Files under this get a full pass on a background goroutine as well, which
	// makes every later chunk a map lookup. Above it, windows stay the only path.
	bgLimit = 2 << 20

	cacheBudget     = 64 << 20
	idleCacheBudget = 8 << 20
)

// classFor maps a chroma token type onto a short CSS class. Short names matter:
// this string repeats once per token across every line we ship.
func classFor(t chroma.TokenType) string {
	switch t {
	case chroma.KeywordType:
		return "kt"
	case chroma.NameAttribute:
		return "na"
	case chroma.NameTag:
		return "nt"
	case chroma.NameDecorator:
		return "nd"
	case chroma.NameClass, chroma.NameNamespace, chroma.NameException:
		return "nc"
	case chroma.NameConstant:
		return "no"
	case chroma.NameProperty, chroma.NameLabel:
		return "np"
	case chroma.Error:
		return "err"
	case chroma.GenericInserted:
		return "gi"
	case chroma.GenericDeleted:
		return "gd"
	case chroma.GenericHeading, chroma.GenericSubheading:
		return "gh"
	case chroma.GenericEmph:
		return "ge"
	case chroma.GenericStrong:
		return "gs"
	}
	switch {
	case t >= 1000 && t < 2000:
		return "k"
	case t >= 2300 && t < 2400:
		return "nf"
	case t >= 2200 && t < 2300:
		return "nv"
	case t >= 2100 && t < 2200:
		return "nb"
	case t >= 2000 && t < 2100:
		return "" // plain Name: the bulk of most files, left unstyled
	case t >= 3100 && t < 3300:
		if t < 3200 {
			return "s"
		}
		return "m"
	case t >= 3000 && t < 3100:
		return "s"
	case t >= 4000 && t < 5000:
		return "o"
	case t >= 5000 && t < 6000:
		return "p"
	case t >= 6100 && t < 6200:
		return "cp"
	case t >= 6000 && t < 7000:
		return "c"
	case t >= 7000 && t < 8000:
		return "g"
	}
	return ""
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func writeHTMLEscaped(b *strings.Builder, s string) {
	last := 0
	for i := 0; i < len(s); i++ {
		var esc string
		switch s[i] {
		case '&':
			esc = "&amp;"
		case '<':
			esc = "&lt;"
		case '>':
			esc = "&gt;"
		default:
			continue
		}
		b.WriteString(s[last:i])
		b.WriteString(esc)
		last = i + 1
	}
	b.WriteString(s[last:])
}

// visualWidth counts runes, expanding tabs to the 4-column stops the UI uses.
func visualWidth(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' {
			n += 4 - n%4
		} else if c < 0x80 {
			n++
		} else {
			// Multibyte UTF-8 fallback from current position
			for _, r := range s[i:] {
				if r == '\t' {
					n += 4 - n%4
				} else {
					n++
				}
			}
			return n
		}
	}
	return n
}

// ---------------------------------------------------------------- document

type Doc struct {
	Lang    string
	MaxCols int
	Total   int

	src     string
	lineOff []int // lineOff[i] is the byte offset of line i; lineOff[Total] == len(src)
	lexer   chroma.Lexer

	mu       sync.RWMutex
	chunks   map[int][]string
	full     bool
	foldKind uint8

	bgOnce sync.Once
	folds  *foldData

	// key identifies this document in the cache so the chunks it accumulates
	// can be charged against the budget as they appear. Sizing a document once
	// at creation undercounts badly: reading a large file end to end stores
	// several times its source size in rendered HTML.
	key   string
	bytes int
}

func NewDoc(src, rel string) *Doc {
	total := strings.Count(src, "\n") + 1
	d := &Doc{
		src: src, Total: total, chunks: map[int][]string{},
		foldKind: foldingKind(rel),
		lineOff:  make([]int, total+1),
		// Source plus line table. Rendered chunks are charged as they appear.
		bytes: len(src) + total*32,
	}
	off := 0
	for i := 0; i < total; i++ {
		d.lineOff[i] = off
		var l string
		if nl := strings.IndexByte(src[off:], '\n'); nl >= 0 {
			l = src[off : off+nl]
			off += nl + 1
		} else {
			l = src[off:]
			off = len(src) + 1
		}
		if n := visualWidth(l); n > d.MaxCols {
			d.MaxCols = n
		}
	}
	if d.MaxCols > maxReportedCols {
		d.MaxCols = maxReportedCols
	}
	d.lineOff[total] = len(src)

	d.lexer = lexers.Match(filepath.Base(rel))
	if d.lexer == nil {
		sample := src
		if len(sample) > 4096 {
			sample = sample[:4096]
		}
		d.lexer = lexers.Analyse(sample)
	}
	if d.lexer == nil {
		d.Lang = "plain text"
	} else {
		d.Lang = d.lexer.Config().Name
		d.lexer = chroma.Coalesce(d.lexer)
	}
	return d
}

// Raw returns the source text of a 1-based line, without its newline.
func (d *Doc) Raw(n int) string {
	if n < 1 || n > d.Total {
		return ""
	}
	start := d.lineOff[n-1]
	end := d.lineOff[n]
	if end > start && end <= len(d.src) && end > 0 && d.src[end-1] == '\n' {
		end--
	}
	if end > len(d.src) {
		end = len(d.src)
	}
	if start > end {
		return ""
	}
	return d.src[start:end]
}

// RawText returns a borrowed source slice for a selected half-open line range.
// It does not tokenize or populate the highlighted-line cache.
func (d *Doc) RawText(start, end int) string {
	if start < 0 {
		start = 0
	}
	if end > d.Total {
		end = d.Total
	}
	if start >= end {
		return ""
	}
	last := d.lineOff[end]
	if end < d.Total && last > d.lineOff[start] && d.src[last-1] == '\n' {
		last--
	}
	return d.src[d.lineOff[start]:last]
}

// Lines returns highlighted HTML for the half-open line range [start, end).
// The second result reports whether every line came from a full-file pass; a
// false means a window guessed at a construct longer than its context and the
// client should come back for it once the background pass lands.
func (d *Doc) Lines(start, end int) ([]string, bool) {
	if start < 0 {
		start = 0
	}
	if end > d.Total {
		end = d.Total
	}
	// Single-chunk files within the window byte cap are lexed in full by chunk(0),
	// so only multi-chunk files need a separate background full-file pass.
	if d.Total > hlChunk && len(d.src) <= bgLimit {
		d.bgOnce.Do(func() { go d.backgroundPass() })
	}
	if start >= end {
		return []string{}, true
	}
	exact := true
	out := make([]string, 0, end-start)
	for c := start / hlChunk; c*hlChunk < end; c++ {
		lines, ok := d.chunk(c)
		exact = exact && ok
		base := c * hlChunk
		lo, hi := start-base, end-base
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		if lo >= hi {
			continue
		}
		out = append(out, lines[lo:hi]...)
	}
	return out, exact
}

// Exact reports whether the full-file pass has finished, and whether one is
// even coming for this file.
func (d *Doc) Exact() (done, coming bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.full, len(d.src) <= bgLimit
}

// chunk returns one hlChunk-sized block of highlighted lines, and whether it
// came from the authoritative full-file pass.
func (d *Doc) chunk(c int) ([]string, bool) {
	d.mu.RLock()
	v, ok := d.chunks[c]
	full := d.full
	d.mu.RUnlock()
	if ok {
		return v, full
	}

	start := c * hlChunk
	end := start + hlChunk
	if end > d.Total {
		end = d.Total
	}
	from := start - hlContext
	if from < 0 {
		from = 0
	}
	to := end + hlContext
	if to > d.Total {
		to = d.Total
	}
	// Trim the context if the window is too big in bytes, then give up on
	// highlighting if the window is still too big without it.
	if d.lineOff[to]-d.lineOff[from] > hlWindowBytes {
		from, to = start, end
	}
	seg := d.src[d.lineOff[from]:d.lineOff[to]]
	var lines []string
	if len(seg) > hlWindowBytes {
		lines = plainFallback(seg, to-from)
	} else {
		lines = d.tokenise(seg, to-from)
	}
	v = lines[start-from : end-from]

	d.mu.Lock()
	if d.full {
		v, full = d.chunks[c], true
		d.mu.Unlock()
		return v, full
	}
	if c == 0 && d.Total <= hlChunk && len(seg) <= hlWindowBytes {
		d.full = true
		full = true
	}
	_, had := d.chunks[c]
	d.chunks[c] = v
	d.mu.Unlock()
	if !had {
		cache.grow(d.key, sizeOfLines(v))
	}
	return v, full
}

func sizeOfLines(lines []string) int {
	n := len(lines) * 16 // slice header and allocator overhead per line
	for _, l := range lines {
		n += len(l)
	}
	return n
}

// backgroundPass lexes the file end to end, which both fixes any window whose
// context was not deep enough and makes every subsequent chunk free.
func (d *Doc) backgroundPass() {
	all := d.tokenise(d.src, d.Total)
	d.mu.Lock()
	before := 0
	for _, v := range d.chunks {
		before += sizeOfLines(v)
	}
	after := 0
	for c := 0; c*hlChunk < d.Total; c++ {
		end := (c + 1) * hlChunk
		if end > d.Total {
			end = d.Total
		}
		d.chunks[c] = all[c*hlChunk : end]
		after += sizeOfLines(d.chunks[c])
	}
	d.full = true
	d.mu.Unlock()
	cache.grow(d.key, after-before)
}

// tokenise renders one contiguous run of source into exactly want lines of HTML.
func (d *Doc) tokenise(src string, want int) []string {
	return highlightLines(d.lexer, src, want)
}

// highlightLines lexes src into exactly want lines of HTML, one <i class=...>
// per token. A nil lexer gives escaped plain text.
func highlightLines(lexer chroma.Lexer, src string, want int) (out []string) {
	out = make([]string, 0, want)
	var b strings.Builder
	b.Grow(256)

	emit := func(v, cls string) {
		if v == "" {
			return
		}
		if cls == "" {
			writeHTMLEscaped(&b, v)
			return
		}
		b.WriteString(`<i class=`)
		b.WriteString(cls)
		b.WriteByte('>')
		writeHTMLEscaped(&b, v)
		b.WriteString(`</i>`)
	}

	if lexer == nil {
		return plainFallback(src, want)
	}

	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return plainFallback(src, want)
	}

	// A lexer can panic on pathological input; a file should never take the
	// server down, so fall back to plain text if that happens.
	defer func() {
		if recover() != nil {
			out = plainFallback(src, want)
		}
	}()

	// Consume the iterator directly, cutting lines as newlines go by. Building
	// the full token slice first would double the allocations for no gain.
	for t := it(); t != chroma.EOF; t = it() {
		cls := classFor(t.Type)
		v := t.Value
		for {
			i := strings.IndexByte(v, '\n')
			if i < 0 {
				emit(v, cls)
				break
			}
			emit(v[:i], cls)
			out = append(out, b.String())
			b.Reset()
			v = v[i+1:]
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return pad(out, want)
}

func plainFallback(src string, want int) []string {
	raw := strings.Split(src, "\n")
	out := make([]string, len(raw))
	for i, l := range raw {
		out[i] = htmlEscaper.Replace(l)
	}
	return pad(out, want)
}

func pad(out []string, want int) []string {
	for len(out) < want {
		out = append(out, "")
	}
	return out[:want]
}

// ------------------------------------------------------------------ cache

type hlCache struct {
	mu    sync.Mutex
	ll    *list.List
	items map[string]*list.Element
	used  int
}

type hlItem struct {
	key string
	val *Doc
}

var cache = &hlCache{ll: list.New(), items: map[string]*list.Element{}}

func (c *hlCache) get(key string) *Doc {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*hlItem).val
	}
	return nil
}

func (c *hlCache) put(key string, v *Doc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return
	}
	c.items[key] = c.ll.PushFront(&hlItem{key: key, val: v})
	c.used += v.bytes
	c.evict()
}

// grow charges newly rendered chunks to a document already in the cache, and
// evicts colder documents if that puts the cache over budget.
func (c *hlCache) grow(key string, delta int) {
	if key == "" || delta == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return
	}
	el.Value.(*hlItem).val.bytes += delta
	c.used += delta
	c.evict()
}

// evict drops the least recently used documents until the cache fits. The most
// recent entry is never evicted, so the document being read always survives.
func (c *hlCache) evict() {
	for c.used > cacheBudget && c.ll.Len() > 1 {
		back := c.ll.Back()
		it := back.Value.(*hlItem)
		c.ll.Remove(back)
		delete(c.items, it.key)
		c.used -= it.val.bytes
	}
}

// remove drops any document whose cache key starts with abs + "|".
func (c *hlCache) remove(abs string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := abs + "|"
	removed := false
	for k, el := range c.items {
		if strings.HasPrefix(k, prefix) || k == abs {
			it := el.Value.(*hlItem)
			c.ll.Remove(el)
			delete(c.items, k)
			c.used -= it.val.bytes
			removed = true
		}
	}
	return removed
}

// Evict drops a file from the syntax highlighting cache by absolute path.
func Evict(abs string) bool {
	return cache.remove(abs)
}

// EvictAll clears the syntax highlighting document cache entirely.
func EvictAll() {
	cache.clear()
}

// Scavenge trims the syntax highlighting cache down to idleCacheBudget by
// evicting the least recently used documents. The most recently used document
// is always retained.
func Scavenge() {
	cache.scavenge(idleCacheBudget)
}

func (c *hlCache) scavenge(budget int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for c.used > budget && c.ll.Len() > 1 {
		back := c.ll.Back()
		it := back.Value.(*hlItem)
		c.ll.Remove(back)
		delete(c.items, it.key)
		c.used -= it.val.bytes
	}
}

func (c *hlCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = map[string]*list.Element{}
	c.used = 0
}

func IsBinary(b []byte) bool {
	n := len(b)
	if n > 8000 {
		n = 8000
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}

// Open loads a file and returns it ready to serve line windows from, memoised
// on path+mtime+size so reopening is a map lookup.
func Open(abs, rel string) (*Doc, error) {
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, fmt.Errorf("is a directory")
	}
	if st.Size() > maxFileBytes {
		return nil, fmt.Errorf("file too large (%d bytes)", st.Size())
	}
	key := fmt.Sprintf("%s|%d|%d", abs, st.ModTime().UnixNano(), st.Size())
	if d := cache.get(key); d != nil {
		return d, nil
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	if IsBinary(data) {
		return nil, fmt.Errorf("binary file")
	}
	d := NewDoc(strings.ReplaceAll(string(data), "\r\n", "\n"), rel)
	d.key = key
	cache.put(key, d)
	return d, nil
}

// ------------------------------------------------------------------ diff

// DiffRow is a single line in a diff hunk.
type DiffRow struct {
	Type    string `json:"type"`              // "ctx", "add", "del"
	OldLine int    `json:"oldLine,omitempty"` // 1-based old line number
	NewLine int    `json:"newLine,omitempty"` // 1-based new line number
	At      int    `json:"at,omitempty"`      // working-tree line for deletions
	Text    string `json:"text"`              // raw line text
	HTML    string `json:"html,omitempty"`    // syntax-highlighted HTML
}

// DiffHunk represents a unified diff hunk with its rows.
type DiffHunk struct {
	OldStart int       `json:"oldStart"`
	NewStart int       `json:"newStart"`
	Section  string    `json:"section,omitempty"`
	Rows     []DiffRow `json:"rows"`
}

var diffHunkRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@[ \t]?(.*)$`)

// HighlightDiff parses unified diff text into hunks and tokenises each line
// with the Chroma lexer matched for rel, returning structured hunks with HTML.
func HighlightDiff(rel, diffText string) []DiffHunk {
	if diffText == "" {
		return []DiffHunk{}
	}
	var hunks []DiffHunk
	var cur *DiffHunk
	oldLine, newLine := 0, 0
	lines := strings.Split(diffText, "\n")
	for _, line := range lines {
		m := diffHunkRe.FindStringSubmatch(line)
		if m != nil {
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[3])
			hunks = append(hunks, DiffHunk{
				OldStart: oldLine,
				NewStart: newLine,
				Section:  m[5],
				Rows:     []DiffRow{},
			})
			cur = &hunks[len(hunks)-1]
			continue
		}
		if cur == nil || line == "" || strings.HasPrefix(line, "\\") {
			continue
		}
		c := line[0]
		body := line[1:]
		if c == '+' {
			cur.Rows = append(cur.Rows, DiffRow{Type: "add", NewLine: newLine, Text: body})
			newLine++
		} else if c == '-' {
			cur.Rows = append(cur.Rows, DiffRow{Type: "del", OldLine: oldLine, At: newLine, Text: body})
			oldLine++
		} else {
			cur.Rows = append(cur.Rows, DiffRow{Type: "ctx", OldLine: oldLine, NewLine: newLine, Text: body})
			oldLine++
			newLine++
		}
	}

	if len(hunks) == 0 {
		return hunks
	}

	lexer := lexers.Match(filepath.Base(rel))
	if lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}

	for hIdx := range hunks {
		hunk := &hunks[hIdx]
		if lexer == nil {
			for rIdx := range hunk.Rows {
				hunk.Rows[rIdx].HTML = htmlEscaper.Replace(hunk.Rows[rIdx].Text)
			}
			continue
		}

		// Reconstruct old-side and new-side lines to tokenise each in its code context.
		var oldRows []string
		var newRows []string
		for _, r := range hunk.Rows {
			switch r.Type {
			case "del":
				oldRows = append(oldRows, r.Text)
			case "add":
				newRows = append(newRows, r.Text)
			case "ctx":
				oldRows = append(oldRows, r.Text)
				newRows = append(newRows, r.Text)
			}
		}

		var oldHTML, newHTML []string
		if len(oldRows) > 0 {
			oldHTML = highlightLines(lexer, strings.Join(oldRows, "\n"), len(oldRows))
		}
		if len(newRows) > 0 {
			newHTML = highlightLines(lexer, strings.Join(newRows, "\n"), len(newRows))
		}

		oldIdx, newIdx := 0, 0
		for rIdx := range hunk.Rows {
			r := &hunk.Rows[rIdx]
			switch r.Type {
			case "del":
				if oldIdx < len(oldHTML) {
					r.HTML = oldHTML[oldIdx]
					oldIdx++
				}
			case "add":
				if newIdx < len(newHTML) {
					r.HTML = newHTML[newIdx]
					newIdx++
				}
			case "ctx":
				if newIdx < len(newHTML) {
					r.HTML = newHTML[newIdx]
					newIdx++
				}
				if oldIdx < len(oldHTML) {
					oldIdx++
				}
			}
		}
	}

	return hunks
}

// ------------------------------------------------------------------ markdown

const (
	// A preview renders the whole file in one pass, unlike the windowed code
	// view, so it stops at a size that still converts in well under a second.
	MaxMarkdownBytes = 4 << 20

	// Fenced blocks larger than this are shown without highlighting.
	maxFenceBytes = 256 << 10
)

func IsMarkdown(rel string) bool {
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

func RenderMarkdown(src []byte) (string, error) {
	src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
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

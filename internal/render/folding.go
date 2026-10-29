package render

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
)

// FoldRange keeps the opening line visible and hides start+1 through end,
// inclusive. Fixed-width pairs cost eight bytes each; no source is copied.
type FoldRange [2]uint32

const (
	foldBraces uint8 = iota + 1
	foldGo
	foldJS
	foldPython
	foldHTML
	foldXML
	maxFoldBytes  = 8 << 20
	maxFoldRanges = 8192
	maxFoldDepth  = 256
)

type foldData struct {
	ready   chan struct{}
	ranges  []FoldRange
	limited bool
	err     error
}

func foldingKind(rel string) uint8 {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return foldGo
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return foldJS
	case ".py", ".pyi":
		return foldPython
	case ".html", ".htm":
		return foldHTML
	case ".xml", ".xhtml", ".xht", ".svg", ".mathml", ".mml", ".xsl", ".xslt", ".xaml", ".axml", ".fxml", ".plist", ".resx", ".xsd", ".wsdl", ".rss", ".atom", ".xlf", ".xliff":
		return foldXML
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".java", ".json", ".jsonc", ".css", ".scss":
		return foldBraces
	}
	return 0
}

// Version identifies the exact cached source snapshot, without exposing paths.
func (d *Doc) Version() string {
	if end := strings.LastIndexByte(d.key, '|'); end >= 0 {
		if start := strings.LastIndexByte(d.key[:end], '|'); start >= 0 {
			return d.key[start+1:]
		}
	}
	return ""
}

// Folds is lazy and single-flight per document. It never invokes Chroma, builds
// an AST, splits the source into strings, or extends the initial file-open path.
// Cancellation leaves the document retryable. Cache eviction owns its lifetime.
func (d *Doc) Folds(ctx context.Context) ([]FoldRange, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if d.foldKind == 0 {
		return nil, false, nil
	}
	d.mu.Lock()
	if f := d.folds; f != nil {
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case <-f.ready:
			return f.ranges, f.limited, f.err
		}
	}
	f := &foldData{ready: make(chan struct{})}
	d.folds = f
	d.mu.Unlock()

	f.ranges, f.limited, f.err = scanFolds(ctx, d.src, d.foldKind)
	d.mu.Lock()
	if f.err != nil {
		d.folds = nil
	}
	close(f.ready)
	d.mu.Unlock()
	if f.err == nil {
		// Include the result, ready channel, and allocator rounding.
		cache.grow(d.key, cap(f.ranges)*8+192)
	}
	return f.ranges, f.limited, f.err
}

// scanFolds runs one bounded lexical pass. On uncertain/unsupported syntax it
// omits folds; display and file bytes are independent of these optional ranges.
func scanFolds(ctx context.Context, src string, kind uint8) ([]FoldRange, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if kind == 0 {
		return nil, false, nil
	}
	if len(src) > maxFoldBytes {
		return nil, true, nil
	}
	if kind == foldPython {
		return scanPythonFolds(ctx, src)
	}
	if kind == foldHTML || kind == foldXML {
		return scanTagFolds(ctx, src, kind == foldHTML)
	}
	type delimiter struct {
		line                   uint32
		ch                     byte
		resume, control, group bool
	}
	var stack [maxFoldDepth]delimiter
	depth, checked := 0, 0
	line := uint32(1)
	var quote byte
	var triple, escaped, blockComment, lineComment, regexClass bool
	regexAllowed, control := true, false
	var groupLine uint32 // Go declaration keyword awaiting its opening parenthesis
	var out []FoldRange
	for i := 0; i < len(src); i++ {
		if i-checked >= 4096 {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			checked = i
		}
		c := src[i]
		if c == '\n' {
			line++
			lineComment = false
		}
		if lineComment {
			continue
		}
		if blockComment {
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				blockComment = false
				i++
			}
			continue
		}
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' && !(quote == '`' && kind == foldGo) {
				escaped = true
				continue
			}
			if quote == '/' {
				if c == '[' {
					regexClass = true
				}
				if c == ']' {
					regexClass = false
				}
				if c == '\n' || (c == '/' && !regexClass) {
					quote = 0
					regexAllowed = false
				}
				continue
			}
			if quote == '`' && kind == foldJS && c == '$' && i+1 < len(src) && src[i+1] == '{' {
				if depth == len(stack) {
					return nil, true, nil
				}
				stack[depth] = delimiter{ch: '{', resume: true}
				depth++
				i++
				quote = 0
				regexAllowed = true
				continue
			}
			if c == quote {
				if triple {
					if i+2 >= len(src) || src[i+1] != c || src[i+2] != c {
						continue
					}
					i += 2
				}
				quote = 0
				triple = false
				regexAllowed = false
			}
			continue
		}
		if c == '/' && i+1 < len(src) {
			if src[i+1] == '/' {
				lineComment = true
				i++
				continue
			}
			if src[i+1] == '*' {
				blockComment = true
				i++
				continue
			}
			if kind == foldJS && regexAllowed {
				quote = '/'
				regexClass = false
				continue
			}
		}
		// C++ raw strings may contain both quotes and braces. The delimiter is
		// bounded by the language to sixteen characters.
		if c == 'R' && kind == foldBraces && i+1 < len(src) && src[i+1] == '"' {
			p := i + 2
			for p < len(src) && p-i <= 18 && src[p] != '(' && src[p] != '\n' {
				p++
			}
			if p < len(src) && src[p] == '(' {
				end := strings.Index(src[p+1:], ")"+src[i+2:p]+"\"")
				if end < 0 {
					break
				}
				end += p + 1 + 1 + (p - i - 2) + 1
				line += uint32(strings.Count(src[i:end], "\n"))
				i = end - 1
				regexAllowed = false
				continue
			}
		}
		if c == '"' || c == '\'' || c == '`' {
			groupLine = 0
			quote = c
			triple = c == '"' && kind == foldBraces && i+2 < len(src) && src[i+1] == c && src[i+2] == c
			if triple {
				i += 2
			}
			continue
		}
		if c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			start := i
			for i+1 < len(src) && foldWordByte(src[i+1]) {
				i++
			}
			word := src[start : i+1]
			groupLine = 0
			if kind == foldGo && (word == "import" || word == "const" || word == "var" || word == "type") {
				groupLine = line
			}
			control = word == "if" || word == "while" || word == "for" || word == "with" || word == "switch" || word == "catch"
			regexAllowed = control || word == "return" || word == "throw" || word == "case" || word == "yield" || word == "await" || word == "typeof" || word == "void" || word == "delete" || word == "in" || word == "of" || word == "else" || word == "do"
			continue
		}
		switch c {
		case '{', '[', '(':
			if depth == len(stack) {
				return nil, true, nil
			}
			stack[depth] = delimiter{line: line, ch: c, control: control}
			if c == '(' && groupLine != 0 {
				stack[depth].line = groupLine
				stack[depth].group = true
			}
			depth++
			groupLine = 0
			control = false
			regexAllowed = true
		case '}', ']', ')':
			groupLine = 0
			if depth == 0 {
				continue
			}
			open := stack[depth-1]
			if open.ch != mapClosingFold(c) {
				return nil, true, nil
			}
			depth--
			if open.resume {
				quote = '`'
				continue
			}
			regexAllowed = open.control || c == '}'
			if open.ch == '{' || open.ch == '[' || open.group {
				end := line
				// Keep a shared closing/header line ("} else {", "}, {")
				// visible so folding one branch never hides the next one.
				for p := i + 1; p < len(src) && src[p] != '\n'; p++ {
					if src[p] != ' ' && src[p] != '\t' && src[p] != '\r' && src[p] != ';' && src[p] != ',' {
						end--
						break
					}
				}
				if end > open.line {
					out = append(out, FoldRange{open.line, end})
				}
				if len(out) == maxFoldRanges {
					return finishFolds(out), true, nil
				}
			}
		default:
			if c != ' ' && c != '\n' && c != '\r' && c != '\t' {
				groupLine = 0
				regexAllowed = c != '.'
				control = false
			}
		}
	}
	return finishFolds(out), false, ctx.Err()
}

func foldWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 128
}

func mapClosingFold(c byte) byte {
	switch c {
	case '}':
		return '{'
	case ']':
		return '['
	default:
		return '('
	}
}

func finishFolds(out []FoldRange) []FoldRange {
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] == out[j][0] {
			return out[i][1] > out[j][1]
		}
		return out[i][0] < out[j][0]
	})
	n := 0
	for _, r := range out {
		if n == 0 || out[n-1][0] != r[0] {
			out[n] = r
			n++
		}
	}
	return out[:n]
}

// Python suites start after a colon outside strings/comments/continuations.
// Blank/comment lines do not dedent, and multiline literals cannot invent suites.
func scanPythonFolds(ctx context.Context, src string) ([]FoldRange, bool, error) {
	type suite struct {
		line   uint32
		indent int
	}
	var stack [maxFoldDepth]suite
	depth, brackets, indent := 0, 0, 0
	var quote byte
	var triple, escaped, continued bool
	var out []FoldRange
	line, lastCode := uint32(0), uint32(0)
	var headerLine uint32
	headerIndent := 0
	for off := 0; off < len(src); {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line++
		end := strings.IndexByte(src[off:], '\n')
		if end < 0 {
			end = len(src)
		} else {
			end += off
		}
		text := src[off:end]
		off = end + 1
		p := 0
		indent = 0
		for p < len(text) && (text[p] == ' ' || text[p] == '\t') {
			if text[p] == '\t' {
				indent += 8 - indent%8
			} else {
				indent++
			}
			p++
		}
		if p == len(text) || (quote == 0 && text[p] == '#') {
			continue
		}
		fresh := quote == 0 && brackets == 0 && !continued
		if fresh {
			headerLine, headerIndent = line, indent
			for depth > 0 && indent <= stack[depth-1].indent {
				depth--
				s := stack[depth]
				if lastCode > s.line {
					out = append(out, FoldRange{s.line, lastCode})
				}
				if len(out) == maxFoldRanges {
					return finishFolds(out), true, nil
				}
			}
		}
		var last byte
		continued = false
		for ; p < len(text); p++ {
			c := text[p]
			if quote != 0 {
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' {
					escaped = true
					continue
				}
				if c == quote {
					if triple {
						if p+2 >= len(text) || text[p+1] != c || text[p+2] != c {
							continue
						}
						p += 2
					}
					quote = 0
					triple = false
				}
				continue
			}
			if c == '#' {
				break
			}
			if c == '"' || c == '\'' {
				quote = c
				triple = p+2 < len(text) && text[p+1] == c && text[p+2] == c
				if triple {
					p += 2
				}
				last = 's'
				continue
			}
			switch c {
			case '(', '[', '{':
				brackets++
			case ')', ']', '}':
				brackets--
			}
			if c != ' ' && c != '\t' && c != '\r' {
				last = c
			}
		}
		lastCode = line
		continued = last == '\\'
		if !triple {
			quote = 0
		}
		escaped = false
		if headerLine > 0 && last == ':' && brackets == 0 && quote == 0 {
			if depth == len(stack) {
				return nil, true, nil
			}
			stack[depth] = suite{headerLine, headerIndent}
			depth++
			headerLine = 0
		}
	}
	for depth > 0 {
		depth--
		s := stack[depth]
		if lastCode > s.line {
			out = append(out, FoldRange{s.line, lastCode})
		}
		if len(out) == maxFoldRanges {
			return finishFolds(out), true, nil
		}
	}
	return finishFolds(out), false, ctx.Err()
}

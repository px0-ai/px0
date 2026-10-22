package render

import (
	"context"
	"strings"
)

// Tag folding is a lexical projection, not a DOM builder. Names borrow source
// slices only during scanning; cached results retain numeric line pairs alone.
// HTML uses ASCII-insensitive names and void/raw-text rules. XML preserves case.
type foldTagScanner struct {
	src  string
	ctx  context.Context
	pos  int
	line uint32
	err  error
}

func (s *foldTagScanner) advance(end int) {
	s.line += uint32(strings.Count(s.src[s.pos:end], "\n"))
	s.pos = end
	s.err = s.ctx.Err()
}

// skipDelimited performs bounded string scans over the already size-limited
// source. Markup inside comments, CDATA and processing instructions is opaque.
func (s *foldTagScanner) skipDelimited(prefix int, terminator string) bool {
	end := strings.Index(s.src[s.pos+prefix:], terminator)
	if end < 0 {
		s.advance(len(s.src))
		return false
	}
	s.advance(s.pos + prefix + end + len(terminator))
	return s.err == nil
}

// Declarations can contain quoted '>' and a DTD internal subset. Never resolve
// entities or external identifiers; just skip their literal source boundaries.
func (s *foldTagScanner) skipDeclaration() {
	var quote byte
	subset := 0
	for i := s.pos + 2; i < len(s.src); i++ {
		if i&4095 == 0 && s.ctx.Err() != nil {
			s.err = s.ctx.Err()
			return
		}
		c := s.src[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[':
			subset++
		case ']':
			if subset > 0 {
				subset--
			}
		case '<':
			if strings.HasPrefix(s.src[i:], "<!--") {
				end := strings.Index(s.src[i+4:], "-->")
				if end < 0 {
					s.advance(len(s.src))
					return
				}
				i += 4 + end + 2
			}
		case '>':
			if subset == 0 {
				s.advance(i + 1)
				return
			}
		}
	}
	s.advance(len(s.src))
}

type foldTag struct {
	name                 string
	line                 uint32
	closing, selfClosing bool
}

func foldTagNameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == ':' || c >= 128
}

func foldTagNameByte(c byte) bool {
	return foldTagNameStart(c) || c >= '0' && c <= '9' || c == '-' || c == '.'
}

func (s *foldTagScanner) readTag() (foldTag, bool) {
	t := foldTag{line: s.line}
	i := s.pos + 1
	if i < len(s.src) && s.src[i] == '/' {
		t.closing = true
		i++
	}
	start := i
	if i >= len(s.src) || !foldTagNameStart(s.src[i]) {
		s.advance(s.pos + 1)
		return t, false
	}
	for i < len(s.src) && foldTagNameByte(s.src[i]) {
		i++
	}
	t.name = s.src[start:i]
	for i < len(s.src) {
		if i&4095 == 0 && s.ctx.Err() != nil {
			s.err = s.ctx.Err()
			return t, false
		}
		switch s.src[i] {
		case '\'', '"':
			end := strings.IndexByte(s.src[i+1:], s.src[i])
			if end < 0 {
				s.advance(len(s.src))
				return t, false
			}
			i += end + 2
		case '>':
			t.selfClosing = i > start && s.src[i-1] == '/'
			s.advance(i + 1)
			return t, s.err == nil
		case '<':
			// An unfinished unquoted tag cannot swallow the rest of the file.
			s.advance(i)
			return t, false
		default:
			i++
		}
	}
	s.advance(len(s.src))
	return t, false
}

func foldTagEqual(a, b string, html bool) bool {
	if !html {
		return a == b
	}
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

func foldHTMLVoid(name string) bool {
	switch len(name) {
	case 2:
		return foldTagEqual(name, "br", true) || foldTagEqual(name, "hr", true)
	case 3:
		return foldTagEqual(name, "img", true) || foldTagEqual(name, "col", true) || foldTagEqual(name, "wbr", true)
	case 4:
		return foldTagEqual(name, "area", true) || foldTagEqual(name, "base", true) || foldTagEqual(name, "link", true) || foldTagEqual(name, "meta", true)
	case 5:
		return foldTagEqual(name, "embed", true) || foldTagEqual(name, "input", true) || foldTagEqual(name, "param", true) || foldTagEqual(name, "track", true)
	case 6:
		return foldTagEqual(name, "source", true)
	}
	return false
}

func foldHTMLRaw(name string) bool {
	return foldTagEqual(name, "script", true) || foldTagEqual(name, "style", true) ||
		foldTagEqual(name, "textarea", true) || foldTagEqual(name, "title", true) ||
		foldTagEqual(name, "xmp", true) || foldTagEqual(name, "iframe", true) ||
		foldTagEqual(name, "noembed", true) || foldTagEqual(name, "noframes", true) || foldTagEqual(name, "plaintext", true)
}

func foldTagSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\f' }

func foldRawClose(src string, pos int, name string) bool {
	if foldTagEqual(name, "plaintext", true) || !strings.HasPrefix(src[pos:], "</") {
		return false
	}
	start, end := pos+2, pos+2+len(name)
	return end < len(src) && foldTagEqual(src[start:end], name, true) && (src[end] == '>' || foldTagSpace(src[end]))
}

// Keep a shared closing line visible: </div><section>, </child></parent>, or
// trailing text must not disappear when only the preceding element is folded.
func (s *foldTagScanner) rangeEnd() uint32 {
	end := s.line
	for i := s.pos; i < len(s.src) && s.src[i] != '\n'; i++ {
		if !foldTagSpace(s.src[i]) {
			return end - 1
		}
	}
	return end
}

func scanTagFolds(ctx context.Context, src string, html bool) ([]FoldRange, bool, error) {
	type element struct {
		name               string
		line               uint32
		html, childrenHTML bool
	}
	var stack [maxFoldDepth]element
	s := foldTagScanner{src: src, ctx: ctx, line: 1}
	depth := 0
	var out []FoldRange
	var raw string
	for s.pos < len(src) && s.err == nil {
		next := strings.IndexByte(src[s.pos:], '<')
		if next < 0 {
			s.advance(len(src))
			break
		}
		if next > 0 {
			s.advance(s.pos + next)
			if s.err != nil {
				break
			}
		}
		if raw != "" && !foldRawClose(src, s.pos, raw) {
			s.advance(s.pos + 1)
			continue
		}
		startLine := s.line
		switch {
		case strings.HasPrefix(src[s.pos:], "<!--"):
			if s.skipDelimited(4, "-->") {
				if end := s.rangeEnd(); end > startLine {
					out = append(out, FoldRange{startLine, end})
				}
			}
		case strings.HasPrefix(src[s.pos:], "<![CDATA["):
			if s.skipDelimited(9, "]]>") {
				if end := s.rangeEnd(); end > startLine {
					out = append(out, FoldRange{startLine, end})
				}
			}
		case strings.HasPrefix(src[s.pos:], "<?"):
			s.skipDelimited(2, "?>")
		case strings.HasPrefix(src[s.pos:], "<!"):
			s.skipDeclaration()
		default:
			tag, ok := s.readTag()
			if !ok {
				continue
			}
			if tag.closing {
				// On malformed/omitted end tags, discard unmatched descendants
				// instead of inventing overlapping ranges. Recovery is bounded.
				for i := depth - 1; i >= 0; i-- {
					if !foldTagEqual(stack[i].name, tag.name, stack[i].html) {
						continue
					}
					if end := s.rangeEnd(); end > stack[i].line {
						out = append(out, FoldRange{stack[i].line, end})
					}
					depth = i
					raw = ""
					break
				}
			} else {
				tagHTML := html
				if depth > 0 {
					tagHTML = stack[depth-1].childrenHTML
				}
				if tag.selfClosing || tagHTML && foldHTMLVoid(tag.name) {
					continue
				}
				if depth == len(stack) {
					return nil, true, nil
				}
				childrenHTML := tagHTML
				if tagHTML && (foldTagEqual(tag.name, "svg", true) || foldTagEqual(tag.name, "math", true)) {
					childrenHTML = false
				}
				if html && !tagHTML && tag.name == "foreignObject" {
					childrenHTML = true
				}
				stack[depth] = element{name: tag.name, line: tag.line, html: tagHTML, childrenHTML: childrenHTML}
				depth++
				if tagHTML && foldHTMLRaw(tag.name) {
					raw = tag.name
				}
			}
		}
		if len(out) == maxFoldRanges {
			return finishFolds(out), true, nil
		}
	}
	if s.err != nil {
		return nil, false, s.err
	}
	return finishFolds(out), false, ctx.Err()
}

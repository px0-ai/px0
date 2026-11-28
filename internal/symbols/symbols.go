package symbols

import (
	"bufio"
	"bytes"
	"sort"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Declaration recognition for go-to-definition. An approximation, not a parser:
// when a word match sits on a line that looks like a top-level declaration of
// that word in its language, it floats above call sites. Anything it misses
// still appears in the reference list below.

var familyDeclTemplates = map[string]string{
	"go":  `^\s*(?:func\s+(?:\([^)]*\)\s*)?NAME\b|type\s+NAME\b|(?:var|const)\s+NAME\b|\tNAME\s*=)`,
	"py":  `^\s*(?:(?:async\s+)?def|class)\s+NAME\b|^NAME\s*(?::[^=]+)?=`,
	"js":  `^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?(?:function\s*\*?\s*NAME\b|class\s+NAME\b|(?:const|let|var|type|interface|enum)\s+NAME\b)|^\s*(?:(?:public|private|protected|static|async|get|set|readonly)\s+)*NAME\s*\([^;]*\{\s*$`,
	"rs":  `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:(?:async|const|unsafe)\s+)*(?:fn|struct|enum|trait|type|const|static|mod|union)\s+NAME\b|^\s*macro_rules!\s*NAME\b`,
	"c":   `^\s*#\s*define\s+NAME\b|^\s*(?:typedef\s+.*\bNAME\s*;|(?:struct|enum|union|class|namespace)\s+NAME\b)|^[A-Za-z_][\w\s\*&<>:]*\bNAME\s*\([^;]*$`,
	"jvm": `^\s*(?:(?:public|private|protected|internal|open|abstract|final|static|override|suspend|data|sealed|value|inline)\s+)*(?:class|interface|enum|record|object|fun|val|var|def|trait|type)\s+NAME\b|^\s*(?:(?:public|private|protected|static|final|synchronized|abstract|native)\s+)+[\w<>?,\[\]\s]+\s+NAME\s*\(`,
	"rb":  `^\s*(?:def\s+(?:self\.)?NAME\b|(?:class|module)\s+NAME\b|NAME\s*=)`,
	"sh":  `^\s*(?:function\s+NAME\b|NAME\s*\(\s*\))`,
	"php": `^\s*(?:(?:public|private|protected|static|abstract|final)\s+)*function\s+&?\s*NAME\b|^\s*(?:(?:abstract|final|readonly)\s+)*(?:class|interface|trait|enum)\s+NAME\b`,
	"lua": `^\s*(?:local\s+)?function\s+(?:[\w.]+\.)?NAME\b|^\s*(?:local\s+)?NAME\s*=\s*function\b`,
	"zig": `^\s*(?:pub\s+)?(?:fn|const|var)\s+NAME\b`,
	"ex":  `^\s*(?:def|defp|defmodule|defmacro|defmacrop|defprotocol)\s+NAME\b`,
	"hs":  `^NAME\s*::|^s*(?:data|type|newtype|class)\s+NAME\b`,
}

var extToFamily = map[string]string{
	".go": "go",
	".py": "py", ".pyi": "py",
	".js": "js", ".jsx": "js", ".mjs": "js", ".cjs": "js",
	".ts": "js", ".tsx": "js", ".mts": "js", ".cts": "js",
	".rs": "rs",
	".c":  "c", ".h": "c", ".cc": "c", ".cpp": "c", ".cxx": "c", ".hpp": "c", ".hh": "c",
	".java": "jvm", ".kt": "jvm", ".kts": "jvm", ".scala": "jvm", ".cs": "jvm", ".swift": "jvm",
	".rb": "rb",
	".sh": "sh", ".bash": "sh", ".zsh": "sh",
	".php": "php",
	".lua": "lua",
	".zig": "zig",
	".ex":  "ex", ".exs": "ex",
	".hs": "hs",
}

func FamilyFor(ext string) string {
	return extToFamily[ext]
}

// IdentRe matches a valid identifier for symbol definition search.
var IdentRe = regexp.MustCompile(`^[A-Za-z_$][\w$]*$`)

func CompileDeclFamily(fam, q string) *regexp.Regexp {
	tmpl, ok := familyDeclTemplates[fam]
	if !ok {
		return nil
	}
	re, err := regexp.Compile(strings.ReplaceAll(tmpl, "NAME", q))
	if err != nil {
		return nil
	}
	return re
}

// DeclPatterns compiles, per extension, a regexp that recognises a line
// declaring ident. Returns nil if ident is not a bare identifier.
func DeclPatterns(ident string) map[string]*regexp.Regexp {
	if !IdentRe.MatchString(ident) {
		return nil
	}
	q := regexp.QuoteMeta(ident)
	compiled := make(map[string]*regexp.Regexp, len(familyDeclTemplates))
	for fam := range familyDeclTemplates {
		if re := CompileDeclFamily(fam, q); re != nil {
			compiled[fam] = re
		}
	}
	out := make(map[string]*regexp.Regexp, len(extToFamily))
	for ext, fam := range extToFamily {
		if re := compiled[fam]; re != nil {
			out[ext] = re
		}
	}
	return out
}

// Symbol represents a single outlined declaration in a file (e.g. function, type, class, heading).
type Symbol struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	End    int    `json:"end,omitempty"`
	Detail string `json:"detail,omitempty"`
	Indent int    `json:"indent"`
}

type outlineRule struct {
	kind string
	re   *regexp.Regexp
}

var outlineRules = map[string][]outlineRule{
	"go": {
		{"func", regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)\s*[\(\[]`)},
		{"type", regexp.MustCompile(`^type\s+([A-Za-z_]\w*)\b`)},
		{"const", regexp.MustCompile(`^const\s+([A-Za-z_]\w*)\b`)},
		{"var", regexp.MustCompile(`^var\s+([A-Za-z_]\w*)\b`)},
	},
	"py": {
		{"class", regexp.MustCompile(`^(\s*)class\s+([A-Za-z_]\w*)\b`)},
		{"def", regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)},
	},
	"js": {
		{"class", regexp.MustCompile(`^(\s*)(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)\b`)},
		{"interface", regexp.MustCompile(`^(\s*)(?:export\s+)?interface\s+([A-Za-z_$][\w$]*)\b`)},
		{"type", regexp.MustCompile(`^(\s*)(?:export\s+)?type\s+([A-Za-z_$][\w$]*)\b`)},
		{"enum", regexp.MustCompile(`^(\s*)(?:export\s+)?(?:const\s+)?enum\s+([A-Za-z_$][\w$]*)\b`)},
		{"func", regexp.MustCompile(`^(\s*)(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)\s*[\(<]`)},
		{"func", regexp.MustCompile(`^(\s*)(?:export\s+)?const\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`)},
	},
	"rs": {
		{"mod", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?mod\s+([A-Za-z_]\w*)\b`)},
		{"struct", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?struct\s+([A-Za-z_]\w*)\b`)},
		{"enum", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?enum\s+([A-Za-z_]\w*)\b`)},
		{"trait", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?(?:unsafe\s+)?trait\s+([A-Za-z_]\w*)\b`)},
		{"impl", regexp.MustCompile(`^(\s*)(?:unsafe\s+)?impl(?:<[^>]*>)?\s+([A-Za-z_][\w:<>, ]*?)\s*\{`)},
		{"fn", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?(?:(?:async|const|unsafe|extern(?:\s+"[^"]*")?)\s+)*fn\s+([A-Za-z_]\w*)\b`)},
		{"type", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?type\s+([A-Za-z_]\w*)\b`)},
		{"const", regexp.MustCompile(`^(\s*)(?:pub(?:\([^)]*\))?\s+)?(?:const|static)\s+([A-Za-z_]\w*)\b`)},
	},
	"c": {
		{"class", regexp.MustCompile(`^(\s*)(?:class|struct|enum(?:\s+class)?|union|namespace)\s+([A-Za-z_]\w*)\s*[:{]`)},
		{"func", regexp.MustCompile(`^([A-Za-z_][\w\s\*&<>:]*?)\b([A-Za-z_~][\w:]*)\s*\([^;]*\{\s*$`)},
		{"macro", regexp.MustCompile(`^\s*#\s*define\s+([A-Za-z_]\w*)\b`)},
	},
	"jvm": {
		{"class", regexp.MustCompile(`^(\s*)(?:(?:public|private|protected|internal|open|abstract|final|static|data|sealed|value)\s+)*(?:class|interface|enum|record|object|trait|struct|actor|extension)\s+([A-Za-z_]\w*)\b`)},
		{"func", regexp.MustCompile(`^(\s*)(?:(?:public|private|protected|internal|open|override|suspend|inline|static|final)\s+)*(?:fun|def|func)\s+(?:<[^>]+>\s+)?([A-Za-z_]\w*)\b`)},
		{"func", regexp.MustCompile(`^(\s*)(?:(?:public|private|protected|static|final|synchronized|abstract|native|virtual|override|async)\s+)+[\w<>?,\[\]]+\s+([A-Za-z_]\w*)\s*\(`)},
	},
	"rb": {
		{"module", regexp.MustCompile(`^(\s*)module\s+([A-Za-z_][\w:]*)\b`)},
		{"class", regexp.MustCompile(`^(\s*)class\s+([A-Za-z_][\w:]*)\b`)},
		{"def", regexp.MustCompile(`^(\s*)def\s+((?:self\.)?[A-Za-z_]\w*[?!]?)\b`)},
	},
	"sh": {
		{"func", regexp.MustCompile(`^(\s*)(?:function\s+([A-Za-z_][\w-]*)|([A-Za-z_][\w-]*)\s*\(\s*\))`)},
	},
	"php": {
		{"class", regexp.MustCompile(`^(\s*)(?:(?:abstract|final|readonly)\s+)*(?:class|interface|trait|enum)\s+([A-Za-z_]\w*)\b`)},
		{"func", regexp.MustCompile(`^(\s*)(?:(?:public|private|protected|static|abstract|final)\s+)*function\s+&?\s*([A-Za-z_]\w*)\s*\(`)},
	},
	"lua": {
		{"func", regexp.MustCompile(`^(\s*)(?:local\s+)?function\s+([A-Za-z_][\w.:]*)\s*\(`)},
	},
	"zig": {
		{"const", regexp.MustCompile(`^(\s*)(?:pub\s+)?const\s+([A-Za-z_]\w*)\s*=\s*(?:struct|enum|union)\b`)},
		{"fn", regexp.MustCompile(`^(\s*)(?:pub\s+)?(?:export\s+|inline\s+)*fn\s+([A-Za-z_]\w*)\s*\(`)},
	},
	"ex": {
		{"module", regexp.MustCompile(`^(\s*)(?:defmodule|defprotocol)\s+([A-Za-z_][\w.]*)\b`)},
		{"def", regexp.MustCompile(`^(\s*)(?:def|defp|defmacro|defmacrop)\s+([A-Za-z_]\w*[?!]?)\b`)},
	},
}

var outlineScannerPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 64*1024)
		return &buf
	},
}

// Outline extracts a symbol list from a file with per-language regexps. This is
// the fallback when no language server is running; it also covers Markdown
// headings, which no LSP serves here.
func Outline(abs, rel string) ([]Symbol, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(rel))
	var out []Symbol

	bufPtr := outlineScannerPool.Get().(*[]byte)
	defer outlineScannerPool.Put(bufPtr)

	if ext == ".md" || ext == ".markdown" {
		src, err := os.ReadFile(abs)
		if err != nil {
			return nil, err
		}
		src = bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
		var newlines []int
		for i, c := range src {
			if c == '\n' {
				newlines = append(newlines, i)
			}
		}
		md := goldmark.New(goldmark.WithExtensions(extension.GFM, extension.Footnote))
		doc := md.Parser().Parse(text.NewReader(src))
		_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
			h, ok := n.(*ast.Heading)
			if !entering || !ok || h.Lines().Len() == 0 {
				return ast.WalkContinue, nil
			}
			line := sort.SearchInts(newlines, h.Lines().At(0).Start) + 1
			name := strings.TrimSpace(string(h.Text(src)))
			out = append(out, Symbol{Name: name, Kind: "heading", Line: line, Indent: h.Level - 1})
			return ast.WalkContinue, nil
		})
		return out, nil
	}

	rules := outlineRules[FamilyFor(ext)]
	if len(rules) == 0 {
		return nil, nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(*bufPtr, 256*1024)
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		for _, r := range rules {
			m := r.re.FindStringSubmatch(text)
			if m == nil {
				continue
			}
			name := m[len(m)-1]
			if name == "" && len(m) >= 3 {
				name = m[len(m)-2] // sh alternation
			}
			indent := 0
			if len(m) >= 3 {
				indent = indentCols(m[1])
			}
			kind := r.kind
			if indent > 0 && (kind == "func" || kind == "def" || kind == "fn") {
				kind = "method"
			}
			out = append(out, Symbol{Name: name, Kind: kind, Line: line, Indent: indent})
			break
		}
	}
	return out, nil
}

func indentCols(ws string) int {
	cols := 0
	for _, c := range ws {
		if c == '\t' {
			cols += 4
		} else {
			cols++
		}
	}
	return cols / 2
}

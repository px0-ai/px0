package main

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---- language families -------------------------------------------------

var extFamily = map[string]string{
	".go": "go",
	".py": "py", ".pyi": "py",
	".js": "js", ".jsx": "js", ".mjs": "js", ".cjs": "js",
	".ts": "js", ".tsx": "js", ".svelte": "js", ".vue": "js",
	".rs":   "rust",
	".java": "jvm", ".kt": "jvm", ".kts": "jvm", ".scala": "jvm", ".groovy": "jvm",
	".cs": "jvm",
	".c":  "c", ".h": "c", ".cc": "c", ".cpp": "c", ".cxx": "c", ".hpp": "c", ".hh": "c", ".m": "c", ".mm": "c",
	".rb":  "rb",
	".php": "php",
	".sh":  "sh", ".bash": "sh", ".zsh": "sh",
	".lua": "lua",
	".ex":  "elixir", ".exs": "elixir",
	".swift": "swift",
	".dart":  "swift",
}

func familyFor(ext string) string {
	if f, ok := extFamily[strings.ToLower(ext)]; ok {
		return f
	}
	return "default"
}

// declTemplates are matched against a whole line; %s is the quoted identifier.
var declTemplates = map[string][]string{
	"go":      {`\b(?:func|type|var|const)\s+(?:\([^)]*\)\s*)?%s\b`, `\b%s\s*:?=[^=]`},
	"py":      {`\b(?:def|class)\s+%s\b`, `^\s*%s\s*(?::[^=]*)?=[^=]`},
	"js":      {`\b(?:function|class|interface|type|enum|const|let|var)\s+%s\b`, `\b%s\s*[:=]\s*(?:async\s*)?(?:function|\(|\w+\s*=>)`, `^\s*(?:async\s+)?%s\s*\(`},
	"rust":    {`\b(?:fn|struct|enum|trait|mod|type|const|static|union|macro_rules!)\s+(?:mut\s+)?%s\b`, `\bimpl(?:<[^>]*>)?\s+%s\b`, `\blet\s+(?:mut\s+)?%s\b`},
	"jvm":     {`\b(?:class|interface|enum|record|object|trait|fun|def|val|var)\s+%s\b`, `\b[\w<>\[\],.?]+\s+%s\s*\(`},
	"c":       {`\b(?:struct|union|enum|class|typedef|namespace)\s+%s\b`, `^[\w\s\*&:<>,~]*\b%s\s*\([^;]*$`, `^\s*#\s*define\s+%s\b`},
	"rb":      {`\b(?:def|class|module)\s+(?:self\.)?%s\b`, `^\s*%s\s*=[^=]`},
	"php":     {`\b(?:function|class|interface|trait|const)\s+%s\b`, `\$%s\s*=[^=]`},
	"sh":      {`^\s*(?:function\s+)?%s\s*\(\s*\)`, `^\s*(?:export\s+)?%s=`},
	"lua":     {`\bfunction\s+[\w.:]*%s\b`, `\blocal\s+%s\b`},
	"elixir":  {`\b(?:def|defp|defmodule|defstruct|defmacro)\s+%s\b`},
	"swift":   {`\b(?:func|class|struct|enum|protocol|extension|let|var|typealias)\s+%s\b`},
	"default": {`\b(?:func|function|def|class|type|struct|interface|enum|fn|const|let|var|module|trait|impl|package)\s+%s\b`},
}

// declPatterns compiles, per extension, a regexp that recognises a line
// declaring ident. Used to float definitions above references in search results.
func declPatterns(ident string) map[string]*regexp.Regexp {
	if !regexp.MustCompile(`^[A-Za-z_$][\w$]*$`).MatchString(ident) {
		return nil
	}
	q := regexp.QuoteMeta(ident)
	byFamily := map[string]*regexp.Regexp{}
	for fam, tpls := range declTemplates {
		parts := make([]string, len(tpls))
		for i, t := range tpls {
			parts[i] = "(?:" + strings.ReplaceAll(t, "%s", q) + ")"
		}
		if re, err := regexp.Compile(strings.Join(parts, "|")); err == nil {
			byFamily[fam] = re
		}
	}
	out := map[string]*regexp.Regexp{"": byFamily["default"]}
	for ext, fam := range extFamily {
		out[ext] = byFamily[fam]
	}
	return out
}

// ---- outline -----------------------------------------------------------

type Symbol struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Indent int    `json:"indent"`
}

type symRule struct {
	re   *regexp.Regexp
	kind string // literal kind, or "$N" to take it from capture group N
	name int    // capture group holding the name
}

func r(kind string, name int, pattern string) symRule {
	return symRule{re: regexp.MustCompile(pattern), kind: kind, name: name}
}

var outlineRules = map[string][]symRule{
	"go": {
		r("func", 2, `^func\s+(\([^)]*\)\s*)?([\w]+)\s*[\(\[]`),
		r("$2", 1, `^type\s+([\w]+)\s+(struct|interface)\b`),
		r("type", 1, `^type\s+([\w]+)\s`),
		r("$1", 2, `^(var|const)\s+([\w]+)\s`),
	},
	"py": {
		r("func", 1, `^\s*(?:async\s+)?def\s+([\w]+)\s*\(`),
		r("class", 1, `^\s*class\s+([\w]+)\s*[\(:]`),
	},
	"js": {
		r("func", 1, `^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([\w$]+)`),
		r("class", 1, `^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([\w$]+)`),
		r("type", 2, `^\s*(?:export\s+)?(?:declare\s+)?(interface|type|enum|namespace)\s+([\w$]+)`),
		r("func", 1, `^\s*(?:export\s+)?(?:const|let|var)\s+([\w$]+)\s*(?::[^=]+)?=\s*(?:async\s*)?(?:function|\([^)]*\)\s*(?::[^=]*)?=>|[\w$]+\s*=>)`),
		r("const", 1, `^\s*(?:export\s+)?(?:const|let|var)\s+([\w$]+)\s*=`),
		r("method", 2, `^\s{2,}(?:(?:public|private|protected|static|readonly|async|get|set)\s+)*([\w$]*\s*)?([\w$]+)\s*\([^)]*\)\s*(?::[^{;]+)?\{`),
	},
	"rust": {
		r("func", 1, `^\s*(?:pub(?:\([^)]*\))?\s+)?(?:const\s+|async\s+|unsafe\s+|extern\s+"[^"]*"\s+)*fn\s+([\w]+)`),
		r("$1", 2, `^\s*(?:pub(?:\([^)]*\))?\s+)?(struct|enum|trait|union|mod|type)\s+([\w]+)`),
		r("impl", 1, `^\s*impl(?:\s*<[^>]*>)?\s+(?:[\w:<>, ]+\s+for\s+)?([\w:]+)`),
		r("macro", 1, `^\s*macro_rules!\s+([\w]+)`),
	},
	"jvm": {
		r("$1", 2, `^\s*(?:(?:public|private|protected|internal|final|abstract|sealed|static|open|data|case)\s+)*(class|interface|enum|record|object|trait|struct)\s+([\w]+)`),
		r("func", 1, `^\s*(?:(?:public|private|protected|internal|static|final|override|suspend|async|virtual)\s+)*(?:fun|def)\s+([\w]+)`),
		r("method", 2, `^\s+(?:(?:public|private|protected|static|final|synchronized|abstract|override|virtual)\s+)+(?:[\w<>\[\],.?]+\s+)?([\w<>\[\],.?]+\s+)?([\w]+)\s*\(`),
	},
	"c": {
		r("$1", 2, `^\s*(?:typedef\s+)?(struct|union|enum|class|namespace)\s+([\w]+)`),
		r("func", 1, `^[\w][\w\s\*&:<>,~]*?([\w~]+)\s*\([^;]*$`),
		r("macro", 1, `^\s*#\s*define\s+([\w]+)`),
	},
	"rb": {
		r("$1", 2, `^\s*(def|class|module)\s+((?:self\.)?[\w?!=]+)`),
	},
	"php": {
		r("$1", 2, `^\s*(?:(?:public|private|protected|static|abstract|final)\s+)*(function|class|interface|trait)\s+([\w]+)`),
	},
	"sh": {
		r("func", 1, `^\s*(?:function\s+)?([\w-]+)\s*\(\s*\)\s*\{`),
	},
	"lua": {
		r("func", 1, `^\s*(?:local\s+)?function\s+([\w.:]+)`),
	},
	"elixir": {
		r("$1", 2, `^\s*(defmodule|def|defp|defmacro|defstruct)\s+([\w.?!]+)`),
	},
	"swift": {
		r("$1", 2, `^\s*(?:(?:public|private|internal|fileprivate|open|final|static|override|@objc)\s+)*(func|class|struct|enum|protocol|extension|typealias)\s+([\w]+)`),
	},
	"default": {
		r("$1", 2, `^\s*(func|function|def|class|type|struct|interface|enum|fn|module|trait)\s+([\w.$:?!-]+)`),
	},
}

var markdownHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*$`)

// Outline extracts a symbol list from a file with per-language regexps. This is
// deliberately approximate: it never blocks, never needs a compiler, and is
// right often enough to navigate by.
func Outline(abs, rel string) ([]Symbol, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(rel))
	var out []Symbol
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)

	if ext == ".md" || ext == ".markdown" {
		for line := 1; sc.Scan(); line++ {
			if m := markdownHeading.FindStringSubmatch(sc.Text()); m != nil {
				out = append(out, Symbol{Name: m[2], Kind: "heading", Line: line, Indent: len(m[1]) - 1})
			}
		}
		return out, sc.Err()
	}

	rules := outlineRules[familyFor(ext)]
	for line := 1; sc.Scan(); line++ {
		text := sc.Text()
		if text == "" || len(text) > 500 {
			continue
		}
		for _, ru := range rules {
			m := ru.re.FindStringSubmatch(text)
			if m == nil || ru.name >= len(m) {
				continue
			}
			name := strings.TrimSpace(m[ru.name])
			if name == "" || isNoiseSymbol(name) {
				continue
			}
			kind := ru.kind
			if strings.HasPrefix(kind, "$") {
				g := int(kind[1] - '0')
				if g < len(m) && m[g] != "" {
					kind = m[g]
				} else {
					kind = "sym"
				}
			}
			indent := len(text) - len(strings.TrimLeft(text, " \t"))
			out = append(out, Symbol{Name: name, Kind: kind, Line: line, Indent: indent})
			break
		}
	}
	return out, sc.Err()
}

var noise = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "return": true,
	"else": true, "catch": true, "try": true, "do": true, "case": true,
	"with": true, "match": true, "defer": true, "go": true, "in": true,
}

func isNoiseSymbol(s string) bool { return noise[s] }

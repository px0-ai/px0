package ignore

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RuleKind selects how a rule is evaluated. Most .gitignore lines are a bare
// name ("node_modules") or a suffix ("*.pyc"), and both can be answered by
// comparing path segments. Only the rest need a regexp.
type RuleKind uint8

const (
	RKRegex     RuleKind = iota // wildcards we cannot shortcut; guarded by a prefix test
	RKSegEq                     // a literal name, matched against each segment
	RKSegSuffix                 // "*suffix", matched against each segment
	RKPathEq                    // a literal path, anchored at the root
)

type Rule struct {
	Kind    RuleKind
	Lit     string         // literal name, suffix or path, for the fast kinds
	Prefix  string         // literal head of an anchored pattern, for RKRegex
	Must    string         // literal run the path must contain, for RKRegex
	Re      *regexp.Regexp // matches the pattern itself
	Sub     *regexp.Regexp // matches anything beneath it
	Negate  bool
	DirOnly bool
}

// Hit reports whether rel is matched by this rule, either as the entry itself
// or as something nested under a matched directory.
func (r *Rule) Hit(rel string, isDir bool) bool {
	switch r.Kind {
	case RKSegEq:
		return r.segMatch(rel, isDir, func(seg string) bool { return seg == r.Lit })
	case RKSegSuffix:
		return r.segMatch(rel, isDir, func(seg string) bool { return strings.HasSuffix(seg, r.Lit) })
	case RKPathEq:
		if rel == r.Lit {
			return !r.DirOnly || isDir
		}
		return strings.HasPrefix(rel, r.Lit) && len(rel) > len(r.Lit) && rel[len(r.Lit)] == '/'
	}
	if r.Prefix != "" && !strings.HasPrefix(rel, r.Prefix) {
		return false
	}
	if r.Must != "" && !strings.Contains(rel, r.Must) {
		return false
	}
	return (r.Re.MatchString(rel) && (!r.DirOnly || isDir)) || r.Sub.MatchString(rel)
}

func (r *Rule) segMatch(rel string, isDir bool, eq func(string) bool) bool {
	start := 0
	for i := 0; i <= len(rel); i++ {
		if i < len(rel) && rel[i] != '/' {
			continue
		}
		last := i == len(rel)
		if eq(rel[start:i]) && (!last || !r.DirOnly || isDir) {
			return true
		}
		start = i + 1
	}
	return false
}

func classify(p string, anchored bool) (RuleKind, string) {
	wild := strings.ContainsAny(p, "*?")
	if anchored {
		if !wild {
			return RKPathEq, p
		}
		return RKRegex, ""
	}
	if !wild {
		return RKSegEq, p
	}
	if strings.HasPrefix(p, "*") && !strings.ContainsAny(p[1:], "*?") {
		return RKSegSuffix, p[1:]
	}
	return RKRegex, ""
}

func literalHead(p string) string {
	if i := strings.IndexAny(p, "*?"); i >= 0 {
		return p[:i]
	}
	return p
}

func literalRun(p string) string {
	best := ""
	for _, part := range strings.FieldsFunc(p, func(r rune) bool { return r == '*' || r == '?' }) {
		part = strings.TrimPrefix(part, "/")
		if len(part) > len(best) {
			best = part
		}
	}
	return best
}

// IgnoreSet is the stack of rules that apply at a given directory, ordered
// outermost-first. Later rules win, which matches git's semantics.
type IgnoreSet struct {
	Rules []Rule
}

var DefaultIgnores = []string{
	".git/", ".hg/", ".svn/", "node_modules/", ".venv/", "venv/", "__pycache__/",
	"target/", "dist/", "build/", ".next/", ".nuxt/", "vendor/", ".idea/", ".vscode/",
	".mypy_cache/", ".pytest_cache/", ".ruff_cache/", ".gradle/", ".tox/",
	"*.pyc", "*.class", "*.o", "*.so", "*.dylib", "*.a", "*.exe", "*.pdb",
	".DS_Store", "*.lock",
}

func New(extra []string) *IgnoreSet {
	s := &IgnoreSet{}
	s.AddPatterns(DefaultIgnores)
	s.AddPatterns(extra)
	return s
}

// Child returns a new set that inherits the parent rules and appends the
// patterns from a .gitignore found in a subdirectory.
func (s *IgnoreSet) Child(patterns []string) *IgnoreSet {
	if len(patterns) == 0 {
		return s
	}
	n := &IgnoreSet{Rules: make([]Rule, len(s.Rules), len(s.Rules)+len(patterns))}
	copy(n.Rules, s.Rules)
	n.AddPatterns(patterns)
	return n
}

func (s *IgnoreSet) AddPatterns(patterns []string) {
	for _, p := range patterns {
		if r, ok := CompilePattern(p); ok {
			s.Rules = append(s.Rules, r)
		}
	}
}

// Match reports whether rel (slash-separated, relative to the scan root)
// is ignored. isDir enables dir-only rules.
func (s *IgnoreSet) Match(rel string, isDir bool) bool {
	ignored := false
	for i := range s.Rules {
		r := &s.Rules[i]
		if r.Hit(rel, isDir) {
			ignored = !r.Negate
		}
	}
	return ignored
}

func CompilePattern(p string) (Rule, bool) {
	return compile(p, true)
}

func CompilePatternRegex(p string) (Rule, bool) {
	return compile(p, false)
}

func compile(p string, fast bool) (Rule, bool) {
	p = strings.TrimRight(p, " ")
	if p == "" || strings.HasPrefix(p, "#") {
		return Rule{}, false
	}
	var r Rule
	if strings.HasPrefix(p, "!") {
		r.Negate = true
		p = p[1:]
	}
	if strings.HasSuffix(p, "/") {
		r.DirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	anchored := strings.Contains(strings.TrimSuffix(p, "/"), "/")
	p = strings.TrimPrefix(p, "/")

	if fast {
		if kind, lit := classify(p, anchored); kind != RKRegex {
			r.Kind, r.Lit = kind, lit
			return r, true
		}
		if anchored {
			r.Prefix = literalHead(p)
		}
		r.Must = literalRun(p)
	}

	var b strings.Builder
	b.WriteString("^")
	if !anchored {
		b.WriteString("(?:.*/)?")
	}
	for i := 0; i < len(p); i++ {
		switch c := p[i]; c {
		case '*':
			if i+1 < len(p) && p[i+1] == '*' {
				if i+2 < len(p) && p[i+2] == '/' {
					b.WriteString("(?:.*/)?")
					i += 2
				} else {
					b.WriteString(".*")
					i++
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	body := b.String()
	re, err := regexp.Compile(body + "$")
	if err != nil {
		return Rule{}, false
	}
	sub, err := regexp.Compile(body + "/.*$")
	if err != nil {
		return Rule{}, false
	}
	r.Re, r.Sub = re, sub
	return r, true
}

// ReadGitignore returns the raw patterns in dir/.gitignore, prefixed so they
// resolve against the scan root rather than the directory they were found in.
func ReadGitignore(dir, relDir string) []string {
	f, err := os.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if relDir == "" {
			out = append(out, line)
			continue
		}
		neg := strings.HasPrefix(line, "!")
		line = strings.TrimPrefix(line, "!")
		scoped := relDir + "/" + strings.TrimPrefix(line, "/")
		if neg {
			scoped = "!" + scoped
		}
		out = append(out, scoped)
	}
	return out
}

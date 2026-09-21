package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A single .gitignore rule compiled to a regexp over slash-separated paths
// relative to the directory the .gitignore lives in.
// ruleKind selects how a rule is evaluated. Most .gitignore lines are a bare
// name ("node_modules") or a suffix ("*.pyc"), and both can be answered by
// comparing path segments. Only the rest need a regexp, which matters: a repo
// with a couple of hundred patterns otherwise spends most of an index walk
// backtracking through them.
type ruleKind uint8

const (
	rkRegex     ruleKind = iota // wildcards we cannot shortcut; guarded by a prefix test
	rkSegEq                     // a literal name, matched against each segment
	rkSegSuffix                 // "*suffix", matched against each segment
	rkPathEq                    // a literal path, anchored at the root
)

type rule struct {
	kind    ruleKind
	lit     string         // literal name, suffix or path, for the fast kinds
	prefix  string         // literal head of an anchored pattern, for rkRegex
	must    string         // literal run the path must contain, for rkRegex
	re      *regexp.Regexp // matches the pattern itself
	sub     *regexp.Regexp // matches anything beneath it
	negate  bool
	dirOnly bool
}

// hit reports whether rel is matched by this rule, either as the entry itself
// or as something nested under a matched directory.
func (r *rule) hit(rel string, isDir bool) bool {
	switch r.kind {
	case rkSegEq:
		return r.segMatch(rel, isDir, func(seg string) bool { return seg == r.lit })
	case rkSegSuffix:
		return r.segMatch(rel, isDir, func(seg string) bool { return strings.HasSuffix(seg, r.lit) })
	case rkPathEq:
		if rel == r.lit {
			return !r.dirOnly || isDir
		}
		return strings.HasPrefix(rel, r.lit) && len(rel) > len(r.lit) && rel[len(r.lit)] == '/'
	}
	// An anchored pattern is rooted, so both of its regexps must begin with the
	// same literal head. Rejecting on that first keeps the backtracking engine
	// away from the overwhelming majority of paths.
	if r.prefix != "" && !strings.HasPrefix(rel, r.prefix) {
		return false
	}
	// Every non-wildcard run of the pattern is matched literally, so the
	// longest one has to appear somewhere in the path. This is what rescues
	// "**/"-prefixed patterns, which have no usable head.
	if r.must != "" && !strings.Contains(rel, r.must) {
		return false
	}
	return (r.re.MatchString(rel) && (!r.dirOnly || isDir)) || r.sub.MatchString(rel)
}

// segMatch walks the "/"-separated segments of rel. A match on the final
// segment is the entry itself, so a "dir/" rule needs it to be a directory. A
// match on any earlier segment is an ancestor directory, which always takes the
// whole subtree with it.
func (r *rule) segMatch(rel string, isDir bool, eq func(string) bool) bool {
	start := 0
	for i := 0; i <= len(rel); i++ {
		if i < len(rel) && rel[i] != '/' {
			continue
		}
		last := i == len(rel)
		if eq(rel[start:i]) && (!last || !r.dirOnly || isDir) {
			return true
		}
		start = i + 1
	}
	return false
}

// classify decides which evaluation a pattern qualifies for. Only "*" and "?"
// are wildcards here; every other character is taken literally, matching what
// compilePattern builds.
func classify(p string, anchored bool) (ruleKind, string) {
	wild := strings.ContainsAny(p, "*?")
	if anchored {
		if !wild {
			return rkPathEq, p
		}
		return rkRegex, ""
	}
	if !wild {
		return rkSegEq, p
	}
	if strings.HasPrefix(p, "*") && !strings.ContainsAny(p[1:], "*?") {
		return rkSegSuffix, p[1:]
	}
	return rkRegex, ""
}

// literalHead returns the leading run of a pattern that contains no wildcard.
func literalHead(p string) string {
	if i := strings.IndexAny(p, "*?"); i >= 0 {
		return p[:i]
	}
	return p
}

// literalRun returns the longest wildcard-free run in a pattern, which any
// matching path must contain verbatim. A leading "/" is dropped: the wildcard
// before it may match nothing, in which case the run starts the path instead of
// following a separator.
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

// ignoreSet is the stack of rules that apply at a given directory, ordered
// outermost-first. Later rules win, which matches git's semantics.
type ignoreSet struct {
	rules []rule
}

var defaultIgnores = []string{
	".git/", ".hg/", ".svn/", "node_modules/", ".venv/", "venv/", "__pycache__/",
	"target/", "dist/", "build/", ".next/", ".nuxt/", "vendor/", ".idea/", ".vscode/",
	".mypy_cache/", ".pytest_cache/", ".ruff_cache/", ".gradle/", ".tox/",
	"*.pyc", "*.class", "*.o", "*.so", "*.dylib", "*.a", "*.exe", "*.pdb",
	".DS_Store", "*.lock",
}

func newIgnoreSet(extra []string) *ignoreSet {
	s := &ignoreSet{}
	s.addPatterns(defaultIgnores)
	s.addPatterns(extra)
	return s
}

// child returns a new set that inherits the parent rules and appends the
// patterns from a .gitignore found in a subdirectory.
func (s *ignoreSet) child(patterns []string) *ignoreSet {
	if len(patterns) == 0 {
		return s
	}
	n := &ignoreSet{rules: make([]rule, len(s.rules), len(s.rules)+len(patterns))}
	copy(n.rules, s.rules)
	n.addPatterns(patterns)
	return n
}

func (s *ignoreSet) addPatterns(patterns []string) {
	for _, p := range patterns {
		if r, ok := compilePattern(p); ok {
			s.rules = append(s.rules, r)
		}
	}
}

// match reports whether rel (slash-separated, relative to the scan root)
// is ignored. isDir enables dir-only rules.
func (s *ignoreSet) match(rel string, isDir bool) bool {
	ignored := false
	for i := range s.rules {
		r := &s.rules[i]
		if r.hit(rel, isDir) {
			ignored = !r.negate
		}
	}
	return ignored
}

func compilePattern(p string) (rule, bool) {
	return compile(p, true)
}

// compilePatternRegex builds the rule without the segment shortcuts. Only the
// differential test uses it, to check the shortcuts against the real thing.
func compilePatternRegex(p string) (rule, bool) {
	return compile(p, false)
}

func compile(p string, fast bool) (rule, bool) {
	p = strings.TrimRight(p, " ")
	if p == "" || strings.HasPrefix(p, "#") {
		return rule{}, false
	}
	var r rule
	if strings.HasPrefix(p, "!") {
		r.negate = true
		p = p[1:]
	}
	if strings.HasSuffix(p, "/") {
		r.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	// A pattern without an interior slash matches at any depth.
	anchored := strings.Contains(strings.TrimSuffix(p, "/"), "/")
	p = strings.TrimPrefix(p, "/")

	if fast {
		if kind, lit := classify(p, anchored); kind != rkRegex {
			r.kind, r.lit = kind, lit
			return r, true
		}
		if anchored {
			r.prefix = literalHead(p)
		}
		r.must = literalRun(p)
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
				// "**/" spans any number of directories, bare "**" spans anything.
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
		return rule{}, false
	}
	sub, err := regexp.Compile(body + "/.*$")
	if err != nil {
		return rule{}, false
	}
	r.re, r.sub = re, sub
	return r, true
}

// readGitignore returns the raw patterns in dir/.gitignore, prefixed so they
// resolve against the scan root rather than the directory they were found in.
func readGitignore(dir, relDir string) []string {
	f, err := os.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseGitignore(f, relDir)
}

func parseGitignore(r io.Reader, relDir string) []string {
	var out []string
	sc := bufio.NewScanner(r)
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
		// Re-anchor: a rule found in sub/ only applies under sub/.
		scoped := relDir + "/" + strings.TrimPrefix(line, "/")
		if neg {
			scoped = "!" + scoped
		}
		out = append(out, scoped)
	}
	return out
}

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"
)

// Match carries the hit already split into the text before it, the matched
// text, and the text after. Sending offsets instead would force the client to
// reconcile Go byte indexes with UTF-16 string indexes, and would not survive
// the snipping we do to keep long lines readable in a narrow panel.
type Match struct {
	Line int    `json:"line"` // 1-based
	Pre  string `json:"pre"`
	Mid  string `json:"mid"`
	Post string `json:"post"`
	Def  bool   `json:"def,omitempty"` // line looks like a declaration
}

const (
	snipLead = 32  // start eliding once the match sits this far into the line
	snipKeep = 16  // runes of lead-in kept when we do elide
	snipMax  = 240 // cap on the whole snippet
)

// snip turns one raw line plus a byte range into a display-ready match,
// dropping indentation and keeping the match itself in view.
func snip(line []byte, from, to int) Match {
	pre, mid, post := string(line[:from]), string(line[from:to]), string(line[to:])

	trimmed := strings.TrimLeft(pre, " \t")
	if trimmed != pre {
		pre = trimmed
	}
	if n := utf8.RuneCountInString(pre); n > snipLead {
		r := []rune(pre)
		pre = "…" + string(r[n-snipKeep:])
	}
	if n := utf8.RuneCountInString(mid); n > snipMax {
		mid = string([]rune(mid)[:snipMax]) + "…"
	}
	if budget := snipMax - utf8.RuneCountInString(pre) - utf8.RuneCountInString(mid); budget > 0 {
		if utf8.RuneCountInString(post) > budget {
			post = string([]rune(post)[:budget]) + "…"
		}
	} else {
		post = ""
	}
	return Match{Pre: pre, Mid: mid, Post: strings.TrimRight(post, " \t")}
}

type FileMatches struct {
	Path    string  `json:"path"`
	Matches []Match `json:"matches"`
}

type SearchOpts struct {
	Query     string
	Regex     bool
	Case      bool
	Word      bool
	Glob      string
	MaxFiles  int
	MaxPerFil int
	// classifyDefs marks hits whose line looks like a declaration of Query.
	classifyDefs bool
}

const searchFileCap = 8 << 20 // do not grep blobs

type searcher struct {
	opts   SearchOpts
	re     *regexp.Regexp            // nil for the literal fast path
	lit    []byte                    // literal needle, already case-folded if needed
	glob   *rule                     // path filter, nil when every file is in scope
	defRes map[string]*regexp.Regexp // ext -> declaration pattern for Query
}

func newSearcher(o SearchOpts) (*searcher, error) {
	s := &searcher{opts: o}
	if o.Glob != "" {
		// Match through the rule, not its regexp: a plain pattern such as
		// "server.go" or "web/app.js" is answered without one at all.
		if r, ok := compilePattern(o.Glob); ok {
			s.glob = &r
		}
	}
	if o.Regex || o.Word {
		pat := o.Query
		if !o.Regex {
			pat = regexp.QuoteMeta(pat)
		}
		if o.Word {
			pat = `\b(?:` + pat + `)\b`
		}
		if !o.Case {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, err
		}
		s.re = re
	} else {
		n := o.Query
		if !o.Case {
			n = asciiLowerString(n)
		}
		s.lit = []byte(n)
	}
	if o.classifyDefs {
		s.defRes = declPatterns(o.Query)
	}
	return s, nil
}

// workBuf holds the per-worker scratch that lets a search read and fold a whole
// tree without allocating once per file.
type workBuf struct {
	read  []byte
	lower []byte
}

// keepBuf is the largest scratch a worker holds on to between files. Reusing a
// buffer avoids an allocation per file, but a tree with a few huge files would
// otherwise leave every worker sitting on its largest read forever.
const keepBuf = 1 << 20

func (w *workBuf) release() {
	if cap(w.read) > keepBuf {
		w.read = nil
	}
	if cap(w.lower) > keepBuf {
		w.lower = nil
	}
}

// asciiLower folds A-Z in place into dst, leaving every other byte alone.
//
// Case-insensitive literal search deliberately folds ASCII only. bytes.ToLower
// applies full Unicode folding, which can change a string's byte length (U+0130
// lowercases from two bytes to one), and match offsets taken from the folded
// copy would then no longer line up with the original text. Use regex mode with
// "(?i)" when Unicode folding is what you want.
func asciiLower(dst *[]byte, src []byte) []byte {
	if cap(*dst) < len(src) {
		*dst = make([]byte, len(src)+len(src)/2)
	}
	d := (*dst)[:len(src)]
	for i, c := range src {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		d[i] = c
	}
	return d
}

func asciiLowerString(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// readInto reads a whole file into the worker's buffer, growing it only when a
// bigger file comes along.
func readInto(path string, buf *[]byte, size int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if int64(cap(*buf)) < size+1 {
		*buf = make([]byte, size+1)
	}
	b := (*buf)[:0]
	for {
		if len(b) == cap(b) {
			b = append(b, 0)[:len(b)]
		}
		n, err := f.Read(b[len(b):cap(b)])
		b = b[:len(b)+n]
		if err != nil {
			*buf = b[:0]
			if err == io.EOF {
				return b, nil
			}
			return b, err
		}
	}
}

// scan finds every match in one file's bytes.
func (s *searcher) scan(data []byte, ext string, max int, w *workBuf) []Match {
	hay := data
	if s.re == nil && !s.opts.Case {
		hay = asciiLower(&w.lower, data)
	}
	// Cheap whole-file reject before paying for line splitting.
	if s.re == nil && !bytes.Contains(hay, s.lit) {
		return nil
	}

	var out []Match
	lineNo, start := 1, 0
	defRe := s.defRes[ext]
	for start <= len(data) {
		end := bytes.IndexByte(data[start:], '\n')
		var lineEnd int
		if end < 0 {
			lineEnd = len(data)
		} else {
			lineEnd = start + end
		}
		line, hLine := data[start:lineEnd], hay[start:lineEnd]

		var locs [][]int
		if s.re != nil {
			locs = s.re.FindAllIndex(line, max)
		} else {
			for off := 0; ; {
				i := bytes.Index(hLine[off:], s.lit)
				if i < 0 {
					break
				}
				locs = append(locs, []int{off + i, off + i + len(s.lit)})
				off += i + len(s.lit)
				if len(locs) >= max {
					break
				}
			}
		}
		if len(locs) > 0 {
			isDef := defRe != nil && defRe.Match(line)
			for _, l := range locs {
				m := snip(line, l[0], l[1])
				m.Line, m.Def = lineNo, isDef
				out = append(out, m)
				if len(out) >= max {
					return out
				}
			}
		}
		if end < 0 {
			break
		}
		start = lineEnd + 1
		lineNo++
	}
	return out
}

// Search greps every indexed file in parallel.
func Search(ix *Index, o SearchOpts) ([]FileMatches, bool, error) {
	return SearchContext(context.Background(), ix, o)
}

// SearchContext greps every indexed file in parallel, aborting worker goroutines
// and returning immediately if ctx is cancelled.
func SearchContext(ctx context.Context, ix *Index, o SearchOpts) ([]FileMatches, bool, error) {
	if strings.TrimSpace(o.Query) == "" {
		return nil, false, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if o.MaxFiles == 0 {
		o.MaxFiles = 200
	}
	if o.MaxPerFil == 0 {
		o.MaxPerFil = 50
	}
	s, err := newSearcher(o)
	if err != nil {
		return nil, false, err
	}

	files := ix.Files()
	root := ix.Root()
	// Ignored files are never indexed, but find-in-file on an open one names it
	// exactly. Search that one file rather than finding nothing.
	if f, ok := unindexedTarget(root, files, o.Glob); ok {
		files = []FileEntry{f}
	}
	var (
		mu      sync.Mutex
		results []FileMatches
		hit     int32
		wg      sync.WaitGroup
		jobs    = make(chan int, 512)
	)
	workers := runtime.NumCPU()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := &workBuf{}
			for i := range jobs {
				if ctx.Err() != nil {
					continue // context cancelled: drain jobs and terminate quickly
				}
				f := &files[i]
				if f.Size == 0 || f.Size > searchFileCap {
					continue
				}
				if s.glob != nil && !s.glob.hit(f.Path, false) {
					continue
				}
				if int(atomic.LoadInt32(&hit)) >= o.MaxFiles {
					continue
				}
				data, err := readInto(filepath.Join(root, filepath.FromSlash(f.Path)), &w.read, f.Size)
				if err != nil || isBinary(data) {
					continue
				}
				if ctx.Err() != nil {
					continue
				}
				m := s.scan(data, strings.ToLower(filepath.Ext(f.Path)), o.MaxPerFil, w)
				w.release()
				if len(m) == 0 {
					continue
				}
				atomic.AddInt32(&hit, 1)
				mu.Lock()
				results = append(results, FileMatches{Path: f.Path, Matches: m})
				mu.Unlock()
			}
		}()
	}

	for i := range files {
		select {
		case <-ctx.Done():
			break
		case jobs <- i:
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()

	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	truncated := false
	if len(results) > o.MaxFiles {
		results, truncated = results[:o.MaxFiles], true
	}
	return results, truncated, nil
}

// unindexedTarget reports the file a literal glob names when that file exists
// under root but is not in the index (because .gitignore excludes it). Wildcard
// globs, paths with "." or ".." segments, and anything but a regular file (a
// symlink included) are refused.
func unindexedTarget(root string, files []FileEntry, glob string) (FileEntry, bool) {
	rel := strings.TrimPrefix(glob, "/")
	if rel == "" || strings.ContainsAny(rel, "*?[!\\") || strings.HasSuffix(rel, "/") {
		return FileEntry{}, false
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return FileEntry{}, false
		}
	}
	i := sort.Search(len(files), func(i int) bool { return files[i].Path >= rel })
	if i < len(files) && files[i].Path == rel {
		return FileEntry{}, false // indexed: the normal path handles it
	}
	st, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || !st.Mode().IsRegular() {
		return FileEntry{}, false
	}
	name := rel[strings.LastIndexByte(rel, '/')+1:]
	return FileEntry{
		Path: rel, Name: name, Size: st.Size(),
		lower: strings.ToLower(rel), nameStart: len(rel) - len(name),
	}, true
}

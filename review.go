package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode/utf8"
)

// review.go loads an agent-authored review (docs/internals/review-file-spec.md)
// and serves it to the browser next to the PR-style diff UI.
//
// The review file is read-only input: px0 never writes, renames or deletes it.
// Parsing is strict about shape but forgiving per comment -- one bad comment is
// moved to Rejected with a reason instead of failing the load. Anchoring each
// comment to the code (path exists at head, line in range, anchor text still
// matches) happens once in the background after the listener is up, so a big
// review never delays startup or a request.

//go:embed review.schema.json
var reviewSchema string

const (
	reviewMaxFile         = 2 << 20
	reviewMaxComments     = 2000
	reviewMaxBody         = 16 << 10
	reviewMaxSummary      = 32 << 10
	reviewMaxTitle        = 200
	reviewMaxCommentTitle = 120
	reviewMaxCategory     = 40
	reviewMaxAnchor       = 2000
	reviewMaxRefs         = 8
	reviewMaxPaths        = 500     // distinct files whose content is read to verify anchors
	reviewMaxContent      = 4 << 20 // larger files are left unverified
	reviewRelocateRange   = 25
	reviewContextChars    = 1024
)

var (
	reviewIDRe       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	reviewSeverities = map[string]bool{"blocker": true, "major": true, "minor": true, "nit": true, "question": true, "praise": true}
	reviewVerdicts   = map[string]bool{"approve": true, "request_changes": true, "comment": true}
)

// reviewGenerator says what wrote the review. SessionID is accepted so a later
// change can hand a thread the writer's session; today it is kept server-side
// and never sent to the browser or put in a prompt.
type reviewGenerator struct {
	Agent     string `json:"agent,omitempty"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
}

// reviewPRRef names the pull request a review belongs to: its number on the
// repository's origin remote, or its full URL.
type reviewPRRef struct {
	Number int
	URL    string
}

func (r *reviewPRRef) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*r = reviewPRRef{Number: n}
		return nil
	}
	var u string
	if err := json.Unmarshal(b, &u); err == nil {
		*r = reviewPRRef{URL: u}
		return nil
	}
	return errors.New("pr must be a number or a pull request URL")
}

func (r reviewPRRef) MarshalJSON() ([]byte, error) {
	if r.URL != "" {
		return json.Marshal(r.URL)
	}
	return json.Marshal(r.Number)
}

// reviewMeta is every top-level field except the comments.
type reviewMeta struct {
	Version     int              `json:"version"`
	Title       string           `json:"title,omitempty"`
	Summary     string           `json:"summary,omitempty"`
	Base        string           `json:"base,omitempty"`
	Head        string           `json:"head,omitempty"`
	BaseSHA     string           `json:"baseSHA,omitempty"`
	HeadSHA     string           `json:"headSHA,omitempty"`
	Verdict     string           `json:"verdict,omitempty"`
	PR          *reviewPRRef     `json:"pr,omitempty"`
	GeneratedBy *reviewGenerator `json:"generatedBy,omitempty"`
	CreatedAt   string           `json:"createdAt,omitempty"`
}

// reviewPRChanged reports whether two reviews name different pull requests.
func reviewPRChanged(a, b *reviewPRRef) bool {
	if a == nil || b == nil {
		return a != b
	}
	return *a != *b
}

// reviewPRTarget turns the file's pr into a provider and target. A bare number
// is looked up on root's origin remote and rewritten to its pull request URL.
func reviewPRTarget(ref *reviewPRRef, root string) (GitProvider, PRTarget, error) {
	rawURL := ref.URL
	if rawURL == "" {
		host, p, ok := remoteHostPath(gitRemoteURL(root, ""))
		if !ok || p == "" {
			return nil, PRTarget{}, fmt.Errorf("pr %d needs an origin remote on GitHub; give the pull request URL instead", ref.Number)
		}
		rawURL = fmt.Sprintf("https://%s/%s/pull/%d", host, p, ref.Number)
	}
	provider, target, ok := DetectPRURL(rawURL)
	if !ok {
		return nil, PRTarget{}, fmt.Errorf("pr %q is not a pull request URL px0 recognises", truncateBytes(rawURL, 200))
	}
	return provider, target, nil
}

// reviewRepoTop is the top of the git repository containing dir, symlinks resolved.
func reviewRepoTop(dir string) (string, error) {
	info := gitProbe(dir)
	if !info.ok || info.toplevel == "" {
		return "", errors.New("-review needs to run inside a git repository")
	}
	top := info.toplevel
	if resolved, err := filepath.EvalSymlinks(top); err == nil {
		top = resolved
	}
	return top, nil
}

// reviewRef points at code related to a comment, possibly on another revision.
// It is validated and shown; its code is not fetched yet.
type reviewRef struct {
	Rev     string `json:"rev,omitempty"`
	Path    string `json:"path"`
	Line    int    `json:"line,omitempty"`
	EndLine int    `json:"endLine,omitempty"`
	Label   string `json:"label,omitempty"`
}

// reviewComment carries the fields an agent writes, then the ones px0 computes
// (Kind, Status, OrigLine, OutsideDiff). The computed ones are reset after
// decoding so a file cannot claim a status for itself.
type reviewComment struct {
	ID        string      `json:"id"`
	Body      string      `json:"body"`
	Path      string      `json:"path,omitempty"`
	Line      int         `json:"line,omitempty"`
	EndLine   int         `json:"endLine,omitempty"`
	Side      string      `json:"side,omitempty"`
	Severity  string      `json:"severity,omitempty"`
	Category  string      `json:"category,omitempty"`
	Title     string      `json:"title,omitempty"`
	Anchor    string      `json:"anchor,omitempty"`
	InReplyTo string      `json:"inReplyTo,omitempty"`
	Refs      []reviewRef `json:"refs,omitempty"`

	Kind        string `json:"kind"`                  // general | file | line
	Status      string `json:"status,omitempty"`      // anchored | moved | unanchored | unverified | out_of_range
	OrigLine    int    `json:"origLine,omitempty"`    // where the agent said it was, when px0 moved or demoted it
	OutsideDiff bool   `json:"outsideDiff,omitempty"` // file exists at head but the diff does not touch it

	inherits bool // a reply with no location of its own: takes its parent's
}

// inheritLocation gives a reply that names no location of its own its parent's:
// where the parent sits, and how that was checked.
func (c *reviewComment) inheritLocation(p *reviewComment) {
	c.Path, c.Line, c.EndLine, c.Side = p.Path, p.Line, p.EndLine, p.Side
	c.Status, c.OrigLine, c.OutsideDiff = p.Status, p.OrigLine, p.OutsideDiff
}

// reviewRejected is a comment that was not shown, and why.
type reviewRejected struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// reviewDoc is a parsed review: the accepted comments, what was rejected while
// parsing, and notes about fields that were cut down or ignored.
type reviewDoc struct {
	reviewMeta
	Comments []reviewComment
	Rejected []reviewRejected
	Warnings []string
}

// truncateBytes cuts s to at most n bytes without splitting a UTF-8 character.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// cleanReviewPath accepts only a clean, repo-relative, slash-separated path.
// Unlike Server.safePath it never touches the disk and does not tidy what it is
// given: the path is only ever looked up in a git tree, and "a/../b" from an
// agent is a mistake to report, not something to quietly resolve.
func cleanReviewPath(p string) (string, bool) {
	if p == "" || strings.ContainsAny(p, "\x00\r\n\\") {
		return "", false
	}
	p = strings.TrimPrefix(p, "./")
	if strings.HasPrefix(p, "/") {
		return "", false
	}
	c := path.Clean(p)
	if c == "." || c == ".." || strings.HasPrefix(c, "../") || c != p {
		return "", false
	}
	return c, true
}

// readReviewSource reads the review from a file path, or stdin for "-".
func readReviewSource(src string) ([]byte, error) {
	var r io.Reader = os.Stdin
	if src != "-" {
		f, err := os.Open(src)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, reviewMaxFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > reviewMaxFile {
		return nil, fmt.Errorf("review file is larger than %d MiB", reviewMaxFile>>20)
	}
	return data, nil
}

// parseReview decodes and validates a review. Whole-file problems are errors;
// per-comment problems land in doc.Rejected.
func parseReview(data []byte) (*reviewDoc, error) {
	var in struct {
		reviewMeta
		Comments []json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, fmt.Errorf("invalid review file: %w", err)
	}
	if in.Version == 0 {
		return nil, errors.New(`review file has no "version" (expected 1)`)
	}
	if in.Version != 1 {
		return nil, fmt.Errorf("unsupported review version %d (this px0 reads version 1)", in.Version)
	}
	doc := &reviewDoc{reviewMeta: in.reviewMeta}
	if len(doc.Title) > reviewMaxTitle {
		doc.Title = truncateBytes(doc.Title, reviewMaxTitle)
		doc.Warnings = append(doc.Warnings, "title was truncated to 200 bytes")
	}
	if len(doc.Summary) > reviewMaxSummary {
		doc.Summary = truncateBytes(doc.Summary, reviewMaxSummary)
		doc.Warnings = append(doc.Warnings, "summary was truncated to 32 KiB")
	}
	for name, sha := range map[string]*string{"baseSHA": &doc.BaseSHA, "headSHA": &doc.HeadSHA} {
		if *sha != "" && !(validSHA(*sha) && len(*sha) >= 7) {
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("ignored %s %q: expected 7 to 40 hex digits", name, truncateBytes(*sha, 40)))
			*sha = ""
		}
	}
	if doc.Verdict != "" && !reviewVerdicts[doc.Verdict] {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("ignored unknown verdict %q", truncateBytes(doc.Verdict, 40)))
		doc.Verdict = ""
	}
	if pr := doc.PR; pr != nil {
		switch {
		case pr.URL == "" && pr.Number < 1:
			doc.Warnings = append(doc.Warnings, fmt.Sprintf("ignored pr %d: expected a positive number", pr.Number))
			doc.PR = nil
		case pr.URL != "":
			if _, _, ok := DetectPRURL(pr.URL); !ok {
				doc.Warnings = append(doc.Warnings, fmt.Sprintf("ignored pr %q: not a pull request URL px0 recognises", truncateBytes(pr.URL, 200)))
				doc.PR = nil
			}
		}
	}

	seen := map[string]bool{}
	for i, raw := range in.Comments {
		if i >= reviewMaxComments {
			doc.Rejected = append(doc.Rejected, reviewRejected{
				ID:     "#" + fmt.Sprint(i+1),
				Reason: fmt.Sprintf("%d comments beyond the %d comment limit were ignored", len(in.Comments)-i, reviewMaxComments),
			})
			break
		}
		var c reviewComment
		if err := json.Unmarshal(raw, &c); err != nil {
			doc.Rejected = append(doc.Rejected, reviewRejected{ID: "#" + fmt.Sprint(i+1), Reason: "not a valid comment: " + err.Error()})
			continue
		}
		c.Kind, c.Status, c.OrigLine, c.OutsideDiff = "", "", 0, false
		label := c.ID
		if label == "" {
			label = "#" + fmt.Sprint(i+1)
		}
		if reason := validateReviewComment(&c, seen); reason != "" {
			doc.Rejected = append(doc.Rejected, reviewRejected{ID: label, Reason: reason})
			continue
		}
		seen[c.ID] = true
		doc.Comments = append(doc.Comments, c)
	}
	return doc, nil
}

// validateReviewComment checks one comment and normalises it in place. seen
// holds the ids accepted so far, which is what duplicate and inReplyTo checks
// need.
func validateReviewComment(c *reviewComment, seen map[string]bool) string {
	if !reviewIDRe.MatchString(c.ID) {
		return "id must match [A-Za-z0-9._-]{1,64}"
	}
	if seen[c.ID] {
		return "duplicate id"
	}
	if strings.TrimSpace(c.Body) == "" {
		return "body is required"
	}
	if len(c.Body) > reviewMaxBody {
		return "body is larger than 16 KiB"
	}
	if len(c.Title) > reviewMaxCommentTitle || len(c.Category) > reviewMaxCategory || len(c.Anchor) > reviewMaxAnchor {
		return "title, category or anchor is over its length limit"
	}
	c.Side = strings.ToUpper(c.Side)
	switch c.Side {
	case "":
		c.Side = "RIGHT"
	case "RIGHT", "LEFT":
	default:
		return "side must be RIGHT or LEFT"
	}
	c.Severity = strings.ToLower(c.Severity)
	switch {
	case c.Severity == "":
		c.Severity = "minor"
	case !reviewSeverities[c.Severity]:
		return "unknown severity " + fmt.Sprintf("%q", truncateBytes(c.Severity, 40))
	}
	if c.Line < 0 || c.EndLine < 0 {
		return "line numbers must be positive"
	}
	if c.EndLine > 0 && c.Line == 0 {
		return "endLine requires line"
	}
	if c.EndLine > 0 && c.EndLine < c.Line {
		return "endLine is before line"
	}
	if c.EndLine == c.Line {
		c.EndLine = 0
	}
	if c.Path != "" {
		p, ok := cleanReviewPath(c.Path)
		if !ok {
			return "path must be a clean repo-relative path"
		}
		c.Path = p
	} else if c.Line > 0 {
		return "line requires path"
	}
	if c.InReplyTo != "" {
		if c.InReplyTo == c.ID || !seen[c.InReplyTo] {
			return "inReplyTo must name an earlier comment"
		}
		c.inherits = c.Path == ""
	}
	if len(c.Refs) > reviewMaxRefs {
		return "more than 8 refs"
	}
	for i := range c.Refs {
		r := &c.Refs[i]
		p, ok := cleanReviewPath(r.Path)
		if !ok {
			return "ref path must be a clean repo-relative path"
		}
		r.Path = p
		if r.Line < 0 || r.EndLine < 0 || (r.EndLine > 0 && r.EndLine < r.Line) || (r.EndLine > 0 && r.Line == 0) {
			return "ref has an invalid line range"
		}
		if len(r.Label) > reviewMaxCommentTitle || strings.HasPrefix(r.Rev, "-") || strings.ContainsAny(r.Rev, "\x00\r\n") {
			return "ref has an invalid rev or label"
		}
	}
	c.Kind = reviewKind(c)
	return ""
}

func reviewKind(c *reviewComment) string {
	switch {
	case c.Path == "":
		return "general"
	case c.Line == 0:
		return "file"
	}
	return "line"
}

// ---------------------------------------------------------------- anchoring

// normalizeAnchorLine collapses whitespace so indentation and trailing
// whitespace never decide whether an anchor matches.
func normalizeAnchorLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// normalizeAnchor splits an anchor into normalised lines, dropping blank lines
// at either end.
func normalizeAnchor(a string) []string {
	lines := strings.Split(strings.ReplaceAll(a, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = normalizeAnchorLine(lines[i])
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// windowEquals reports whether the normalised lines starting at the 1-based
// line start are exactly want.
func windowEquals(norm []string, start int, want []string) bool {
	if start < 1 || start-1+len(want) > len(norm) {
		return false
	}
	for i, w := range want {
		if norm[start-1+i] != w {
			return false
		}
	}
	return true
}

// locateAnchor checks an anchor against a file's normalised lines. It returns
// the status and, for "moved", the new first line. A match at the stated line
// is "anchored"; otherwise a single match within reviewRelocateRange lines is
// "moved"; no match, or an ambiguous one, is "unanchored".
func locateAnchor(norm []string, line int, anchor []string) (status string, newLine int) {
	if len(anchor) == 0 {
		return "unverified", line
	}
	if windowEquals(norm, line, anchor) {
		return "anchored", line
	}
	found := 0
	at := 0
	for s := line - reviewRelocateRange; s <= line+reviewRelocateRange; s++ {
		if s == line || s < 1 {
			continue
		}
		if windowEquals(norm, s, anchor) {
			found++
			at = s
		}
	}
	if found == 1 {
		return "moved", at
	}
	return "unanchored", line
}

// ---------------------------------------------------------------- state

// reviewEnv is what a review is checked against: the checkout, the head it is
// written for, and the merge-base that "LEFT" line numbers refer to.
type reviewEnv struct {
	root       string
	headSHA    string
	baseCommit string
	diffFiles  func() map[string]string
}

// reviewState holds the current review and re-reads its file when it changes.
type reviewState struct {
	mu  sync.Mutex
	src string // path as given ("-" for stdin)
	abs string // absolute path, for prompts; "" for stdin
	sig string // mtime+size of the last read

	doc      *reviewDoc
	env      reviewEnv
	gen      int
	resolved bool
	loadErr  string
	announce bool // print what the first resolve pass rejects (set by main; once)

	comments  []reviewComment
	rejected  []reviewRejected
	stale     bool
	staleNote string
}

// loadReview reads and parses a review source. A whole-file problem is an error.
func loadReview(src string) (*reviewState, error) {
	data, err := readReviewSource(src)
	if err != nil {
		return nil, err
	}
	doc, err := parseReview(data)
	if err != nil {
		return nil, err
	}
	rs := &reviewState{src: src, doc: doc}
	if src != "-" {
		if abs, err := filepath.Abs(src); err == nil {
			rs.abs = abs
		}
		rs.sig = fileSig(src)
	} else {
		rs.sig = "stdin"
	}
	// Comments are shown once the background pass has checked them against the
	// code (bind starts it), so one about to be rejected never flashes up.
	rs.rejected = append([]reviewRejected(nil), doc.Rejected...)
	return rs, nil
}

// fileSig identifies a version of the review file by mtime and size, which is
// what changes when an agent rewrites it.
func fileSig(src string) string {
	fi, err := os.Stat(src)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d-%d", fi.ModTime().UnixNano(), fi.Size())
}

// initialReviewComments is the parsed comments as written, with replies placed
// where their parents say. The checks in resolve refine it.
func initialReviewComments(doc *reviewDoc) []reviewComment {
	out := append([]reviewComment(nil), doc.Comments...)
	byID := map[string]*reviewComment{}
	for i := range out {
		c := &out[i]
		if c.inherits {
			if p := byID[c.InReplyTo]; p != nil {
				c.inheritLocation(p)
			}
		}
		c.Kind = reviewKind(c)
		byID[c.ID] = c
	}
	return out
}

// bind attaches the checkout the review is verified against and starts the
// background anchor pass.
func (rs *reviewState) bind(env reviewEnv) {
	rs.mu.Lock()
	rs.env = env
	rs.mu.Unlock()
	rs.start()
}

func (rs *reviewState) start() {
	rs.mu.Lock()
	rs.gen++
	gen, doc, env := rs.gen, rs.doc, rs.env
	rs.resolved = false
	rs.mu.Unlock()
	go rs.resolve(gen, doc, env)
}

// refresh re-reads the file when its mtime or size changed. It is cheap enough
// to call on every request; a file that disappears or stops parsing keeps the
// last good review on screen and reports the problem.
func (rs *reviewState) refresh() {
	if rs.src == "-" {
		return
	}
	sig := fileSig(rs.src)
	rs.mu.Lock()
	unchanged := sig == rs.sig
	rs.mu.Unlock()
	if unchanged {
		return
	}
	data, err := readReviewSource(rs.src)
	var doc *reviewDoc
	if err == nil {
		doc, err = parseReview(data)
	}
	rs.mu.Lock()
	rs.sig = sig
	if err != nil {
		rs.loadErr = "could not reload the review: " + err.Error()
		rs.mu.Unlock()
		return
	}
	rs.loadErr = ""
	if reviewPRChanged(rs.doc.PR, doc.PR) {
		doc.Warnings = append(doc.Warnings, "pr changed in the file; restart px0 to open the other pull request")
	}
	rs.doc = doc
	env := rs.env
	if env.root == "" {
		// Nothing to check against: show the comments as written.
		rs.comments = initialReviewComments(doc)
		rs.rejected = append([]reviewRejected(nil), doc.Rejected...)
		rs.resolved = true
	}
	rs.mu.Unlock()
	if env.root != "" {
		rs.start() // the previous, checked comments stay up until this finishes
	}
}

type reviewFileKey struct{ rev, path string }

// resolve runs once per load, off the request path. It rejects comments whose
// file is not in the commit they refer to, then checks line ranges and anchors
// against the file's content there.
func (rs *reviewState) resolve(gen int, doc *reviewDoc, env reviewEnv) {
	comments := initialReviewComments(doc)
	rejected := append([]reviewRejected(nil), doc.Rejected...)

	var headFiles, baseFiles map[string]bool
	needHead, needBase := false, false
	for _, c := range comments {
		if c.Path == "" {
			continue
		}
		if c.Side == "LEFT" {
			needBase = true
		} else {
			needHead = true
		}
	}
	if needHead {
		headFiles = gitTreeFiles(env.root, env.headSHA)
	}
	if needBase {
		baseFiles = gitTreeFiles(env.root, env.baseCommit)
	}

	// Which (rev, path) pairs are worth reading: line comments only, capped.
	revFor := func(c *reviewComment) string {
		if c.Side == "LEFT" {
			return env.baseCommit
		}
		return env.headSHA
	}
	wanted := map[reviewFileKey]bool{}
	var order []reviewFileKey
	for i := range comments {
		c := &comments[i]
		if c.Line == 0 || c.Path == "" {
			continue
		}
		files := headFiles
		if c.Side == "LEFT" {
			files = baseFiles
		}
		if files != nil && !files[c.Path] {
			continue
		}
		k := reviewFileKey{revFor(c), c.Path}
		if !wanted[k] && len(order) < reviewMaxPaths {
			wanted[k] = true
			order = append(order, k)
		}
	}
	norm := map[reviewFileKey][]string{}
	var nmu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, runtime.NumCPU())
	for _, k := range order {
		wg.Add(1)
		sem <- struct{}{}
		go func(k reviewFileKey) {
			defer wg.Done()
			defer func() { <-sem }()
			lines := gitFileLines(env.root, k.rev, k.path, reviewMaxContent)
			if lines == nil {
				return
			}
			n := make([]string, len(lines))
			for i, l := range lines {
				n[i] = normalizeAnchorLine(l)
			}
			nmu.Lock()
			norm[k] = n
			nmu.Unlock()
		}(k)
	}
	wg.Wait()

	var diffFiles map[string]string
	if env.diffFiles != nil {
		diffFiles = env.diffFiles()
	}

	kept := make([]reviewComment, 0, len(comments))
	index := map[string]int{} // id -> position in kept
	for i := range comments {
		c := comments[i]
		if c.inherits {
			pi, ok := index[c.InReplyTo]
			if !ok {
				rejected = append(rejected, reviewRejected{ID: c.ID, Reason: "the comment it replies to was rejected"})
				continue
			}
			c.inheritLocation(&kept[pi])
			c.Kind = reviewKind(&c)
			index[c.ID] = len(kept)
			kept = append(kept, c)
			continue
		}
		if c.Path != "" {
			files, label := headFiles, "head"
			if c.Side == "LEFT" {
				files, label = baseFiles, "the merge-base"
			}
			if files != nil && !files[c.Path] {
				rejected = append(rejected, reviewRejected{ID: c.ID, Reason: "path not in " + label})
				continue
			}
			if diffFiles != nil {
				_, inDiff := diffFiles[c.Path]
				c.OutsideDiff = !inDiff
			}
		}
		if c.Line > 0 {
			n, ok := norm[reviewFileKey{revFor(&c), c.Path}]
			last := c.Line
			if c.EndLine > 0 {
				last = c.EndLine
			}
			switch {
			case !ok:
				c.Status = "unverified"
			case last > len(n):
				c.Status = "out_of_range"
				c.OrigLine = c.Line
				c.Line, c.EndLine = 0, 0
			default:
				anchor := normalizeAnchor(c.Anchor)
				status, at := locateAnchor(n, c.Line, anchor)
				c.Status = status
				switch status {
				case "moved":
					c.OrigLine = c.Line
					c.Line = at
					if c.EndLine > 0 {
						c.EndLine = at + len(anchor) - 1
						if c.EndLine == c.Line {
							c.EndLine = 0
						}
					}
				case "unanchored":
					c.OrigLine = c.Line
					c.Line, c.EndLine = 0, 0
				}
			}
		}
		c.Kind = reviewKind(&c)
		index[c.ID] = len(kept)
		kept = append(kept, c)
	}

	stale, note := false, ""
	if doc.HeadSHA != "" && env.headSHA != "" && !shaPrefixMatch(doc.HeadSHA, env.headSHA) {
		stale = true
		note = fmt.Sprintf("review was written against %s, but head is %s", shortSHA(doc.HeadSHA), shortSHA(env.headSHA))
	}

	rs.mu.Lock()
	if rs.gen != gen {
		rs.mu.Unlock()
		return // a newer load superseded this one
	}
	rs.comments, rs.rejected = kept, rejected
	rs.stale, rs.staleNote = stale, note
	rs.resolved = true
	announce := rs.announce
	rs.announce = false
	rs.mu.Unlock()

	if announce {
		for _, r := range rejected[len(doc.Rejected):] {
			uiStatus("warn", fmt.Sprintf("review: ignored comment %s: %s", r.ID, r.Reason), "", 0, os.Stderr)
		}
		if stale {
			uiStatus("warn", "review: "+note, "", 0, os.Stderr)
		}
	}
}

func shaPrefixMatch(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

// ---------------------------------------------------------------- output

type reviewSnapshot struct {
	Version     int              `json:"version"`
	Title       string           `json:"title,omitempty"`
	Summary     string           `json:"summary,omitempty"`
	Base        string           `json:"base,omitempty"`
	Head        string           `json:"head,omitempty"`
	HeadSHA     string           `json:"headSHA,omitempty"`
	Verdict     string           `json:"verdict,omitempty"`
	GeneratedBy *reviewGenerator `json:"generatedBy,omitempty"`
	CreatedAt   string           `json:"createdAt,omitempty"`
	Stale       bool             `json:"stale"`
	StaleNote   string           `json:"staleNote,omitempty"`
	Resolved    bool             `json:"resolved"`
	LoadError   string           `json:"loadError,omitempty"`
	Warnings    []string         `json:"warnings,omitempty"`
	Comments    []reviewComment  `json:"comments"`
	Rejected    []reviewRejected `json:"rejected"`
	etag        string
}

func (rs *reviewState) snapshot() reviewSnapshot {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	s := reviewSnapshot{
		Version:   rs.doc.Version,
		Title:     rs.doc.Title,
		Summary:   rs.doc.Summary,
		Base:      rs.doc.Base,
		Head:      rs.doc.Head,
		HeadSHA:   rs.env.headSHA,
		Verdict:   rs.doc.Verdict,
		CreatedAt: rs.doc.CreatedAt,
		Stale:     rs.stale,
		StaleNote: rs.staleNote,
		Resolved:  rs.resolved,
		LoadError: rs.loadErr,
		Warnings:  rs.doc.Warnings,
		Comments:  append([]reviewComment{}, rs.comments...),
		Rejected:  append([]reviewRejected{}, rs.rejected...),
		etag:      fmt.Sprintf(`W/"%s-%d-%t-%t"`, rs.sig, rs.gen, rs.resolved, rs.loadErr != ""),
	}
	if g := rs.doc.GeneratedBy; g != nil {
		s.GeneratedBy = &reviewGenerator{Agent: g.Agent, Model: g.Model} // the session id stays server-side
	}
	return s
}

// counts tallies accepted comments by severity.
func (s reviewSnapshot) counts() map[string]int {
	m := map[string]int{}
	for _, c := range s.Comments {
		m[c.Severity]++
	}
	return m
}

// contextBlock is the paragraph a thread's first prompt gets about the review.
// It names the full file for harnesses that can read it, gives the summary, and
// says plainly that the review is a claim to verify.
func (rs *reviewState) contextBlock() string {
	s := rs.snapshot()
	var b strings.Builder
	b.WriteString("An automated review of this change is loaded in px0")
	if s.Title != "" {
		fmt.Fprintf(&b, ": %q", truncateBytes(s.Title, 200))
	}
	n := len(s.Comments)
	fmt.Fprintf(&b, " (%d comment", n)
	if n != 1 {
		b.WriteString("s")
	}
	cnt := s.counts()
	if cnt["blocker"] > 0 || cnt["major"] > 0 {
		fmt.Fprintf(&b, ", %d blocker, %d major", cnt["blocker"], cnt["major"])
	}
	b.WriteString(")")
	if s.GeneratedBy != nil && s.GeneratedBy.Agent != "" {
		fmt.Fprintf(&b, " written by %s", truncateBytes(s.GeneratedBy.Agent, 60))
	}
	b.WriteString(". ")
	if rs.abs != "" {
		fmt.Fprintf(&b, "The full review file is %s. ", rs.abs)
	}
	if s.Summary != "" {
		fmt.Fprintf(&b, "Its summary: %s ", truncateBytes(strings.TrimSpace(s.Summary), reviewContextChars))
	}
	b.WriteString("Treat the review as claims to check against the code, not as ground truth; the user may be asking about one of its comments, quoted in their message.")
	return b.String()
}

// ---------------------------------------------------------------- HTTP

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.review == nil {
		fail(w, http.StatusNotFound, "no review loaded")
		return
	}
	s.review.refresh()
	snap := s.review.snapshot()
	w.Header().Set("ETag", snap.etag)
	w.Header().Set("Cache-Control", "no-cache")
	if r.Header.Get("If-None-Match") == snap.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, snap)
}

// SetReview attaches an agent-authored review to this session and verifies it
// against the checked-out head. It needs the PR/local-review session SetPR
// installed, because that is what defines the head and merge-base.
func (s *Server) SetReview(rs *reviewState) {
	s.review = rs
	if rs == nil || s.pr == nil {
		return
	}
	p := s.pr
	p.mu.Lock()
	env := reviewEnv{root: p.worktree, headSHA: p.meta.HeadSHA, baseCommit: p.diffBase}
	p.mu.Unlock()
	env.diffFiles = s.ix.PRFiles
	rs.bind(env)
}

// ---------------------------------------------------------------- local sessions

// defaultReviewBase picks a base when neither the flags nor the file name one.
func defaultReviewBase(root string) string {
	for _, c := range []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"} {
		if _, err := revParseCommit(root, c); err == nil {
			return c
		}
	}
	return ""
}

// prepareLocalReview builds the session for a review of two local revisions.
// When head is what is already checked out, the repository is used in place;
// otherwise head is checked out into a throwaway worktree under the OS temp
// directory, removed by prSession.Close.
func prepareLocalReview(doc *reviewDoc, baseFlag, headFlag, dir string) (*prSession, error) {
	top, err := reviewRepoTop(dir)
	if err != nil {
		return nil, err
	}
	first := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	headRev := first(headFlag, doc.Head, "HEAD")
	baseRev := first(baseFlag, doc.Base, defaultReviewBase(top))
	if baseRev == "" {
		return nil, errors.New("no base revision: set \"base\" in the review file or pass -base")
	}
	headSHA, err := revParseCommit(top, headRev)
	if err != nil {
		return nil, err
	}
	baseSHA, err := revParseCommit(top, baseRev)
	if err != nil {
		return nil, err
	}
	mergeBase := gitMergeBase(top, headSHA, baseSHA)
	if mergeBase == "" {
		return nil, fmt.Errorf("%s and %s share no history", headRev, baseRev)
	}

	title := doc.Title
	if title == "" {
		title = "Local review"
	}
	author := ""
	if doc.GeneratedBy != nil {
		author = doc.GeneratedBy.Agent
	}
	p := &prSession{
		local:    true,
		meta:     PRMeta{Title: title, Author: author, State: "open", BaseRef: baseRev, HeadRef: headRev, HeadSHA: headSHA},
		diffBase: mergeBase,
	}
	if cur, err := revParseCommit(top, "HEAD"); err == nil && cur == headSHA {
		p.worktree, p.inPlace = top, true
		return p, nil
	}
	tmp, err := tempDirResolved("px0-review-*")
	if err != nil {
		return nil, err
	}
	if out, err := exec.Command("git", "-C", top, "worktree", "add", "--detach", tmp, headSHA).CombinedOutput(); err != nil {
		os.RemoveAll(tmp)
		return nil, fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	p.worktree, p.srcRepo = tmp, top
	return p, nil
}

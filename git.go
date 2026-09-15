package main

import (
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// gitDisabled turns off all git awareness (the -no-git flag). Like uiQuiet, a
// process-wide switch set once in main before anything reads it.
var gitDisabled bool

type gitInfo struct {
	ok       bool
	toplevel string // repo root as git reports it (symlinks resolved)
}

var (
	gitMu    sync.Mutex
	gitCache = map[string]gitInfo{}
)

// gitAvailable reports whether the git binary is on PATH and root sits inside a
// working tree. Memoized per root: detection shells out once. Fails quiet -- no
// git, no repo, or -no-git all yield false, never an error.
func gitAvailable(root string) bool { return gitProbe(root).ok }

func gitProbe(root string) gitInfo {
	if gitDisabled {
		return gitInfo{}
	}
	gitMu.Lock()
	defer gitMu.Unlock()
	if info, ok := gitCache[root]; ok {
		return info
	}
	var info gitInfo
	if _, err := exec.LookPath("git"); err == nil {
		if out, err := exec.Command("git", "-C", root, "rev-parse", "--show-toplevel").Output(); err == nil {
			info = gitInfo{ok: true, toplevel: strings.TrimSpace(string(out))}
		}
	}
	gitCache[root] = info
	return info
}

// gitStatus maps repo-relative-to-served-root path -> single-letter status for
// every file git considers changed. Uses porcelain v2 -z, the stable
// null-delimited format. Fails quiet: nil on any error, no repo, or disabled.
func gitStatus(root string) map[string]string {
	info := gitProbe(root)
	if !info.ok {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "status", "--porcelain=v2", "-z").Output()
	if err != nil {
		return nil
	}
	// Porcelain paths are relative to the repo root regardless of -C, so strip
	// the served root's offset within the repo to match the index's keys.
	prefix := ""
	if rel, err := filepath.Rel(info.toplevel, root); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	key := func(p string) (string, bool) {
		if prefix == "" {
			return p, true
		}
		if !strings.HasPrefix(p, prefix) {
			return "", false // outside the served subtree
		}
		return p[len(prefix):], true
	}

	status := map[string]string{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" {
			continue
		}
		switch f[0] {
		case '?': // "? <path>"
			if k, ok := key(f[2:]); ok {
				status[k] = "U" // untracked
			}
		case '1': // "1 <XY> <sub> <mH> <mI> <mW> <hH> <hI> <path>"
			p := strings.SplitN(f, " ", 9)
			if len(p) == 9 {
				if k, ok := key(p[8]); ok {
					status[k] = mapXY(p[1])
				}
			}
		case '2': // "2 <XY> ... <Rscore> <path>", then original path in the next field
			p := strings.SplitN(f, " ", 10)
			if len(p) == 10 {
				if k, ok := key(p[9]); ok {
					status[k] = mapXY(p[1])
				}
			}
			i++ // the original path follows as its own NUL-terminated field
		case 'u': // "u <XY> <sub> <m1> <m2> <m3> <mW> <h1> <h2> <h3> <path>"
			p := strings.SplitN(f, " ", 11)
			if len(p) == 11 {
				if k, ok := key(p[10]); ok {
					status[k] = "!" // unmerged / conflict
				}
			}
		}
	}
	if len(status) == 0 {
		return nil
	}
	return status
}

// mapXY collapses a porcelain v2 two-letter XY code (X=index, Y=worktree) into
// a single status letter, preferring the staged side when both are set.
func mapXY(xy string) string {
	if len(xy) < 2 {
		return "M"
	}
	c := xy[0]
	if c == '.' {
		c = xy[1]
	}
	switch c {
	case 'A':
		return "A"
	case 'D':
		return "D"
	case 'R':
		return "R"
	case 'C':
		return "C"
	case 'U':
		return "!" // unmerged / conflict
	default: // M (modified), T (typechange) and anything else read as modified
		return "M"
	}
}

// parseNewStart pulls newStart out of a hunk header "@@ -a,b +c,d @@".
func parseNewStart(hdr string) int {
	i := strings.IndexByte(hdr, '+')
	if i < 0 {
		return 1
	}
	rest := hdr[i+1:]
	if end := strings.IndexAny(rest, ", "); end >= 0 {
		rest = rest[:end]
	}
	if n, err := strconv.Atoi(rest); err == nil {
		return n
	}
	return 1
}

// gitCommit represents a single commit with basic metadata.
type gitCommit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	RelTime string `json:"relTime"`
	ISOTime string `json:"isoTime"`
	Subject string `json:"subject"`
}

// gitUpstreamLog returns commits ahead of the tracking branch, and the
// tracking ref itself (e.g. "origin/master" — the remote qualifies it, since
// a bare local branch name like "master" doesn't say what it's ahead of).
// Resolves the upstream via git rev-parse --symbolic-full-name @{u}. If no
// upstream is configured, returns nil, "", false. If an upstream exists,
// returns commits (newest first, even if empty), the upstream ref, and true.
func gitUpstreamLog(root string, n int) (commits []gitCommit, upstream string, hasUpstream bool) {
	if !gitAvailable(root) {
		return nil, "", false
	}
	// Resolve tracking branch
	upstreamOut, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Output()
	if err != nil {
		return nil, "", false // no upstream
	}
	upstream = strings.TrimSpace(string(upstreamOut))
	if upstream == "@{u}" { // not resolved (shouldn't happen, but be safe)
		return nil, "", false
	}

	// Get log: hash, short hash, author, relative time, ISO time, subject
	// Format: %H%x1f%h%x1f%an%x1f%ar%x1f%aI%x1f%s
	out, err := exec.Command("git", "-C", root, "log", "-n", strconv.Itoa(n),
		"--format=%H%x1f%h%x1f%an%x1f%ar%x1f%aI%x1f%s",
		upstream+"..HEAD").Output()
	if err != nil {
		// Even if log fails, we have confirmed upstream exists
		return []gitCommit{}, upstream, true
	}

	if len(out) == 0 {
		return []gitCommit{}, upstream, true
	}

	var result []gitCommit
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 6 {
			continue
		}
		result = append(result, gitCommit{
			Hash:    parts[0],
			Short:   parts[1],
			Author:  parts[2],
			RelTime: parts[3],
			ISOTime: parts[4],
			Subject: parts[5],
		})
	}
	return result, upstream, true
}

// gitParent returns the parent commit hash of ref, or the empty-tree constant
// if ref is a root commit (has no parent).
const emptyTreeHash = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// hashRe gates every ref that reaches exec.Command as a bare revision arg.
var hashRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)

func gitParent(root, ref string) string {
	if !gitAvailable(root) {
		return emptyTreeHash
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "--verify", ref+"^1").Output()
	if err != nil {
		// Root commit (no parent) or invalid ref -> return empty tree
		return emptyTreeHash
	}
	return strings.TrimSpace(string(out))
}

// gitCommitFiles returns a map of file paths to status codes for a given commit
// hash. Validates the hash against a hex regexp first (closes injection vector).
// Uses the same mapXY logic as gitStatus for status codes.
func gitCommitFiles(root, hash string) map[string]string {
	if !gitAvailable(root) {
		return nil
	}
	if !hashRe.MatchString(hash) {
		return map[string]string{}
	}

	// hash^..hash covers the normal case in one subprocess; only a root commit
	// (no parent) needs the empty-tree fallback below.
	out, err := exec.Command("git", "-C", root, "diff", "--name-status", hash+"^.."+hash).Output()
	if err != nil {
		out, err = exec.Command("git", "-C", root, "diff", "--name-status", emptyTreeHash+".."+hash).Output()
		if err != nil {
			return map[string]string{}
		}
	}

	result := map[string]string{}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}
		// Format: "X<tab>path" or "X<tab>oldpath<tab>newpath" for renames/copies,
		// where X may carry a trailing similarity score (e.g. "R100"). Split on
		// tab only -- paths may contain spaces.
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		path := parts[len(parts)-1] // for rename/copy, take the new path (last field)
		c := parts[0][:1]
		result[path] = mapXY(c + c) // mapXY reads X (or Y if X is '.'); doubling the letter reuses it as-is
	}
	return result
}

// gitDiff returns the unified diff of relpath. When ref is empty, diffs against
// HEAD (working tree); when ref is non-empty, validates it as a hash and diffs
// the commit against its parent. Fails quiet -> "".
func gitDiff(root, ref, relpath string) string {
	if !gitAvailable(root) {
		return ""
	}

	// Handle ref="" case: diff against HEAD (old behavior)
	if ref == "" {
		out, err := exec.Command("git", "-C", root, "diff", "--no-color", "HEAD", "--", relpath).Output()
		if err != nil {
			return ""
		}
		return string(out)
	}

	// Validate ref as a hash
	if !hashRe.MatchString(ref) {
		return ""
	}

	parent := gitParent(root, ref)
	out, err := exec.Command("git", "-C", root, "diff", "--no-color", parent+".."+ref, "--", relpath).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// shortstatRe matches git's "--shortstat" summary line, anchored to a line
// start with its leading space so it can't accidentally match text inside a
// commit body.
var shortstatRe = regexp.MustCompile(`(?m)^ (\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

// gitCommitDetail returns a commit's body (subject excluded, already known to
// callers via gitUpstreamLog) and its files/insertions/deletions shortstat, in
// one subprocess call. ok is false only on error (bad hash, no repo); an empty
// body or a commit touching zero files are valid, non-error results.
func gitCommitDetail(root, hash string) (body string, files, ins, del int, ok bool) {
	if !gitAvailable(root) || !hashRe.MatchString(hash) {
		return "", 0, 0, 0, false
	}
	out, err := exec.Command("git", "-C", root, "log", "-1", "--format=%b", "--shortstat", hash).Output()
	if err != nil {
		return "", 0, 0, 0, false
	}
	text := string(out)
	loc := shortstatRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return strings.TrimSpace(text), 0, 0, 0, true // no shortstat line: zero files changed
	}
	body = strings.TrimSpace(text[:loc[0]])
	files, _ = strconv.Atoi(text[loc[2]:loc[3]])
	if loc[4] != -1 {
		ins, _ = strconv.Atoi(text[loc[4]:loc[5]])
	}
	if loc[6] != -1 {
		del, _ = strconv.Atoi(text[loc[6]:loc[7]])
	}
	return body, files, ins, del, true
}

// gitHunks parses the unified diff of relpath into change gutter ranges.
// ref="" diffs against HEAD (working tree); ref=<hash> diffs a commit against
// its parent. Fails quiet: all nil.
func gitHunks(root, ref, relpath string) (added, modified, deleted []int) {
	diff := gitDiff(root, ref, relpath)
	if diff == "" {
		return nil, nil, nil
	}
	newLine := 0
	inHunk := false
	// Current block: a maximal run of consecutive '+'/'-' lines.
	dels := 0
	var adds []int
	blockStart := 0 // newLine when the block began (for deletion markers)
	flush := func() {
		switch {
		case dels > 0 && len(adds) > 0:
			modified = append(modified, adds...) // replacement
		case len(adds) > 0:
			added = append(added, adds...) // pure insertion
		case dels > 0:
			deleted = append(deleted, blockStart-1) // pure deletion
		}
		dels, adds = 0, nil
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
			inHunk = true
			newLine = parseNewStart(line)
		case !inHunk, strings.HasPrefix(line, "\\"): // pre-hunk header / "\ No newline"
			// skip: neither +/- nor a new-file line
		case strings.HasPrefix(line, "+"):
			if dels == 0 && len(adds) == 0 {
				blockStart = newLine
			}
			adds = append(adds, newLine)
			newLine++
		case strings.HasPrefix(line, "-"):
			if dels == 0 && len(adds) == 0 {
				blockStart = newLine
			}
			dels++
		default: // context line (" ...", or the trailing empty split element)
			flush()
			newLine++
		}
	}
	flush()
	return added, modified, deleted
}

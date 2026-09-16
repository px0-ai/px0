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

// gitDiff returns the unified diff of relpath against HEAD. relpath is relative
// to the served root; git resolves it against -C root. Fails quiet -> "".
func gitDiff(root, relpath string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "diff", "--no-color", "HEAD", "--", relpath).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitHunks parses the unified diff of relpath against HEAD into 1-based
// NEW-FILE line numbers for a change gutter: added lines, modified (replaced)
// lines, and one marker per pure-deletion run (the new-file line immediately
// preceding the removed run; 0 means "before the first line"). Fails quiet:
// empty when git is off/unavailable or the file has no diff (clean/untracked).
func gitHunks(root, relpath string) (added, modified, deleted []int) {
	diff := gitDiff(root, relpath)
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

// blameCommit is one distinct commit referenced by a file's blame, sent once
// per commit (not per line) to keep the payload small on large files.
type blameCommit struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Author  string `json:"author"`
	Time    int64  `json:"time"` // author-time, unix seconds; 0 for uncommitted lines
	Summary string `json:"summary"`
}

const zeroBlameHash = "0000000000000000000000000000000000000000"

// gitBlame runs `git blame --porcelain` on relpath. With no revision given,
// git blames the working tree, so uncommitted edits are attributed to "Not
// Committed Yet" and line numbers line up exactly with the file as displayed
// -- no remapping against the diff/gutter needed. Fails quiet: nil/nil when
// git is off/absent or the path has no blame (untracked, doesn't exist).
func gitBlame(root, relpath string) (commits []blameCommit, lineCommit []int) {
	if !gitAvailable(root) {
		return nil, nil
	}
	out, err := exec.Command("git", "-C", root, "blame", "--porcelain", "--", relpath).Output()
	if err != nil {
		return nil, nil
	}
	return parseBlame(string(out))
}

// blameHeaderRE matches a porcelain blame line-record header: "<sha> <origline>
// <finalline>[ <numlines>]". numlines (present only on a hunk's first line) is
// not needed -- every output line gets its own header regardless, so a plain
// per-line scan is enough.
var blameHeaderRE = regexp.MustCompile(`^([0-9a-f]{40}) \d+ (\d+)`)

// parseBlame turns `git blame --porcelain` output into deduplicated commits
// (full metadata appears only the first time a hash is seen; later lines from
// the same commit repeat just the header) plus a same-length-as-the-file
// commit index per line.
func parseBlame(out string) (commits []blameCommit, lineCommit []int) {
	idx := map[string]int{} // hash -> index into commits
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); {
		m := blameHeaderRE.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		hash, final := m[1], atoiOr0(m[2])
		i++
		meta := map[string]string{}
		for i < len(lines) && !strings.HasPrefix(lines[i], "\t") {
			if sp := strings.IndexByte(lines[i], ' '); sp > 0 {
				meta[lines[i][:sp]] = lines[i][sp+1:]
			}
			i++
		}
		if i < len(lines) {
			i++ // consume the tab-prefixed content line
		}
		ci, ok := idx[hash]
		if !ok {
			ci = len(commits)
			idx[hash] = ci
			author := meta["author"]
			if hash == zeroBlameHash {
				author = "Not Committed Yet"
			}
			commits = append(commits, blameCommit{
				Hash: hash, Short: hash[:7], Author: author,
				Time: atoi64Or0(meta["author-time"]), Summary: meta["summary"],
			})
		}
		for len(lineCommit) < final {
			lineCommit = append(lineCommit, -1)
		}
		lineCommit[final-1] = ci
	}
	return commits, lineCommit
}

func atoiOr0(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func atoi64Or0(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

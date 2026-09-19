package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// gitDisabled turns off all git awareness (the -no-git flag). Like uiQuiet, a
// process-wide switch set once in main before anything reads it.
var gitDisabled bool

var (
	gitCompareBase  = "HEAD"
	gitCompareLabel = "HEAD"
)

type gitInfo struct {
	ok       bool
	toplevel string // repo root as git reports it (symlinks resolved)
	gitdir   string // absolute path to .git directory or file
}

var (
	gitMu    sync.Mutex
	gitCache = map[string]gitInfo{}
)

// gitAvailable reports whether the git binary is on PATH and root sits inside a
// working tree. Memoized per root: detection shells out once. Fails quiet -- no
// git, no repo, or -no-git all yield false, never an error.
func gitAvailable(root string) bool { return gitProbe(root).ok }

// gitDir returns the absolute path to the repository's .git directory.
func gitDir(root string) string { return gitProbe(root).gitdir }

// configureGitDiffBase resolves ref to the merge base it shares with HEAD.
// The immutable commit keeps every status and diff request on the same base
// even if the named branch moves while px0 is running.
func configureGitDiffBase(root, ref string) error {
	gitCompareBase = "HEAD"
	gitCompareLabel = "HEAD"
	if ref == "" {
		return nil
	}
	if !gitAvailable(root) {
		return fmt.Errorf("git is unavailable for this workspace")
	}
	out, err := exec.Command("git", "-C", root, "merge-base", ref, "HEAD").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	base := strings.TrimSpace(string(out))
	if base == "" {
		return fmt.Errorf("git merge-base returned no commit")
	}
	gitCompareBase = base
	gitCompareLabel = ref
	return nil
}

func gitDiffBase() string {
	if gitCompareBase == "" {
		return "HEAD"
	}
	return gitCompareBase
}

func gitDiffBaseLabel() string {
	if gitCompareLabel == "" {
		return "HEAD"
	}
	return gitCompareLabel
}

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
			top := strings.TrimSpace(string(out))
			gd := filepath.Join(top, ".git")
			if gdOut, err := exec.Command("git", "-C", root, "rev-parse", "--git-dir").Output(); err == nil {
				rawGd := strings.TrimSpace(string(gdOut))
				if filepath.IsAbs(rawGd) {
					gd = rawGd
				} else {
					gd = filepath.Join(top, rawGd)
				}
			}
			info = gitInfo{ok: true, toplevel: top, gitdir: gd}
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
	out, err := exec.Command("git", "-C", root, "status", "--porcelain=v2", "-z", "-uall").Output()
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
	if gitDiffBase() != "HEAD" {
		if diffOut, err := exec.Command("git", "-C", root, "diff", "--name-status", "-z", "--find-renames", gitDiffBase(), "--").Output(); err == nil {
			items := strings.Split(string(diffOut), "\x00")
			for i := 0; i < len(items); {
				code := items[i]
				i++
				if code == "" || i >= len(items) {
					continue
				}
				if code[0] == 'R' || code[0] == 'C' {
					i++ // old path
					if i >= len(items) {
						break
					}
				}
				path := items[i]
				i++
				if k, ok := key(path); ok {
					status[k] = mapNameStatus(code[0])
				}
			}
		}
	}
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
					if _, exists := status[k]; !exists {
						status[k] = mapXY(p[1])
					}
				}
			}
		case '2': // "2 <XY> ... <Rscore> <path>", then original path in the next field
			p := strings.SplitN(f, " ", 10)
			if len(p) == 10 {
				if k, ok := key(p[9]); ok {
					if _, exists := status[k]; !exists {
						status[k] = mapXY(p[1])
					}
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

func mapNameStatus(code byte) string {
	switch code {
	case 'A':
		return "A"
	case 'D':
		return "D"
	case 'R':
		return "R"
	case 'C':
		return "C"
	default:
		return "M"
	}
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

// gitDiff returns the unified diff of relpath against the configured base. relpath is relative
// to the served root; git resolves it against -C root. Fails quiet -> "".
func gitDiff(root, relpath string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "diff", "--no-color", gitDiffBase(), "--", relpath).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitHunks parses the unified diff of relpath against the configured base into 1-based
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

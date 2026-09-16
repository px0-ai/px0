package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	prefix := repoPrefix(info, root)
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

// repoPrefix is the served root's path within the repo, slash-terminated, or ""
// when the served root is the repo root. Git reports and accepts paths relative
// to the repo root regardless of -C, so this is the offset that has to come off
// (or go back on) to line up with the index's keys.
func repoPrefix(info gitInfo, root string) string {
	rel, err := filepath.Rel(info.toplevel, root)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel) + "/"
}

// gitShowHead returns the contents of relpath as it stands in HEAD. ok is false
// when git is off/unavailable or the path does not exist in HEAD -- an added or
// untracked file, or the new name of a rename. Fails quiet like the rest.
func gitShowHead(root, relpath string) (string, bool) {
	info := gitProbe(root)
	if !info.ok {
		return "", false
	}
	spec := "HEAD:" + repoPrefix(info, root) + filepath.ToSlash(relpath)
	out, err := exec.Command("git", "-C", root, "show", spec).Output()
	if err != nil {
		return "", false
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n"), true
}

// ChangedFile is one entry of the working tree's changeset against HEAD.
// Old carries the previous path of a rename and is empty otherwise.
type ChangedFile struct {
	Path    string `json:"path"`
	Old     string `json:"old,omitempty"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Deleted int    `json:"deleted"`
}

// gitChangeset inventories everything that differs from HEAD, sorted by path.
// Two sources, because neither is complete on its own: `git diff --numstat`
// carries the line counts and rename detection but is blind to untracked files,
// and porcelain status sees untracked files but counts nothing. An untracked
// file is reported as a whole-file addition -- a new file is the thing most
// worth reviewing, and it is invisible to `git diff HEAD`.
func gitChangeset(root string) []ChangedFile {
	info := gitProbe(root)
	if !info.ok {
		return nil
	}
	status := gitStatus(root)
	prefix := repoPrefix(info, root)
	inSubtree := func(p string) (string, bool) {
		if prefix == "" {
			return p, true
		}
		rest, ok := strings.CutPrefix(p, prefix)
		return rest, ok
	}

	var files []ChangedFile
	seen := map[string]bool{}
	out, err := exec.Command("git", "-C", root, "-c", "core.quotepath=false",
		"diff", "-M", "HEAD", "--numstat", "-z").Output()
	if err != nil {
		return nil
	}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		add, del, rest, ok := cutNumstat(fields[i])
		if !ok {
			continue
		}
		oldPath, newPath := "", rest
		if rest == "" { // a rename: the two paths follow as their own fields
			if i+2 >= len(fields) {
				break
			}
			oldPath, newPath = fields[i+1], fields[i+2]
			i += 2
		}
		key, ok := inSubtree(newPath)
		if !ok {
			continue
		}
		f := ChangedFile{Path: key, Status: status[key], Added: numstat(add), Deleted: numstat(del)}
		if oldPath != "" {
			f.Old = oldPath
			if k, ok := inSubtree(oldPath); ok {
				f.Old = k
			}
			f.Status = "R"
		}
		if f.Status == "" {
			f.Status = "M"
		}
		seen[key] = true
		files = append(files, f)
	}

	for path, st := range status {
		if st != "U" || seen[path] {
			continue
		}
		// Porcelain collapses an untracked directory into a single "dir/" entry, so
		// the files inside would never be listed -- and a directory of new files is
		// exactly what an agent leaves behind. Ask git to name them.
		for _, p := range expandUntracked(root, prefix, path) {
			key, ok := inSubtree(p)
			if !ok || seen[key] {
				continue
			}
			seen[key] = true
			files = append(files, ChangedFile{
				Path: key, Status: "U", Added: countLines(filepath.Join(root, filepath.FromSlash(key))),
			})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// cutNumstat splits an "<added>\t<deleted>\t<path>" --numstat record. rest is
// empty on a rename, where the two paths follow as their own NUL-terminated
// fields instead of being inlined here.
func cutNumstat(f string) (add, del, rest string, ok bool) {
	add, tail, ok := strings.Cut(f, "\t")
	if !ok {
		return "", "", "", false
	}
	del, rest, ok = strings.Cut(tail, "\t")
	return add, del, rest, ok
}

// numstat reads one side of a --numstat pair. Binary files report "-".
func numstat(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// countLines counts the lines of a text file on disk, for the untracked files
// git will not count for us. Binary and oversized files report 0 rather than
// being read into memory.
func countLines(abs string) int {
	st, err := os.Stat(abs)
	if err != nil || st.IsDir() || st.Size() > maxFileBytes {
		return 0
	}
	data, err := os.ReadFile(abs)
	if err != nil || isBinary(data) {
		return 0
	}
	s := strings.ReplaceAll(string(data), "\r\n", "\n")
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// gitChangedLines maps each changed file to the new-file line numbers the
// changeset touches, in one git process for the whole repo. Per-file calls were
// the obvious shape and the wrong one: a dense changeset is dozens of files,
// and a process spawn each is what turns an endpoint into a stall.
//
// -U0 means every hunk header is the change itself. A pure deletion reports a
// zero-length new range; it is attributed to the line it happened after, so the
// symbol that lost the lines is still the one credited with the change.
func gitChangedLines(root string) map[string][]int {
	info := gitProbe(root)
	if !info.ok {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "-c", "core.quotepath=false",
		"diff", "-M", "HEAD", "-U0", "--no-color").Output()
	if err != nil {
		return nil
	}
	prefix := repoPrefix(info, root)
	changed := map[string][]int{}
	cur := ""
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			cur = ""
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" { // the file was deleted: no new-side lines
				continue
			}
			p = strings.TrimPrefix(p, "b/")
			if prefix != "" {
				rest, ok := strings.CutPrefix(p, prefix)
				if !ok {
					continue
				}
				p = rest
			}
			cur = p
		case cur != "" && strings.HasPrefix(line, "@@"):
			start, count, ok := hunkNewRange(line)
			if !ok {
				continue
			}
			if count == 0 { // pure deletion: credit the line it followed
				if start < 1 {
					start = 1
				}
				changed[cur] = append(changed[cur], start)
				continue
			}
			for n := start; n < start+count; n++ {
				changed[cur] = append(changed[cur], n)
			}
		}
	}
	if len(changed) == 0 {
		return nil
	}
	return changed
}

// hunkNewRange pulls "+start,count" out of a hunk header. A missing count means
// one line, which is what unified diff omits it for.
func hunkNewRange(hdr string) (start, count int, ok bool) {
	i := strings.IndexByte(hdr, '+')
	if i < 0 {
		return 0, 0, false
	}
	rest := hdr[i+1:]
	if end := strings.IndexAny(rest, " @"); end >= 0 {
		rest = rest[:end]
	}
	num, cnt, hasCount := strings.Cut(rest, ",")
	start, err := strconv.Atoi(num)
	if err != nil {
		return 0, 0, false
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(cnt); err != nil {
			return 0, 0, false
		}
	}
	return start, count, true
}

// expandUntracked turns one porcelain untracked entry into the repo-relative
// paths it actually covers. Anything not ending in "/" is already a file and
// comes back as-is; a collapsed directory is expanded with ls-files, which
// honours .gitignore the same way status did.
func expandUntracked(root, prefix, path string) []string {
	if !strings.HasSuffix(path, "/") {
		return []string{prefix + path}
	}
	out, err := exec.Command("git", "-C", root, "-c", "core.quotepath=false",
		"ls-files", "--others", "--exclude-standard", "-z", "--", prefix+path).Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// gitRenameSource returns the pre-rename path of relpath, or "" when relpath is
// not the new name of a rename. Only worth consulting when the path is absent
// from HEAD: an added file and the new name of a rename look identical from
// there, and rendering a rename as a whole-file addition is exactly what
// /api/review and /api/diff were disagreeing about.
func gitRenameSource(root, relpath string) string {
	info := gitProbe(root)
	if !info.ok {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "-c", "core.quotepath=false",
		"diff", "-M", "--name-status", "-z", "HEAD").Output()
	if err != nil {
		return ""
	}
	want := repoPrefix(info, root) + filepath.ToSlash(relpath)
	fields := strings.Split(string(out), "\x00")
	// A rename record is three fields: "R<score>", the old path, the new path.
	for i := 0; i+2 < len(fields); i++ {
		if !strings.HasPrefix(fields[i], "R") {
			continue
		}
		if fields[i+2] == want {
			// Callers work in paths relative to the served root, like rel.
			src, ok := strings.CutPrefix(fields[i+1], repoPrefix(info, root))
			if !ok {
				return "" // the old name lives outside the served subtree
			}
			return src
		}
		i += 2
	}
	return ""
}

// gitDiffRenamed renders the diff of a rename. Both paths have to be in the
// pathspec: given only the new name git pairs nothing and reports the file as
// wholly added. gitDiff stays untouched -- handleGutter shares it.
func gitDiffRenamed(root, oldPath, newPath string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "diff", "--no-color", "-M", "HEAD",
		"--", oldPath, newPath).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

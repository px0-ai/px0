package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
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
	return gitStatusAgainst(root, "HEAD")
}

// repoRelKey returns a function mapping a git-porcelain path (always relative
// to the repo toplevel) to a path relative to root, or false when it falls
// outside root's subtree (root may be a subdirectory of the repo).
func repoRelKey(info gitInfo, root string) func(string) (string, bool) {
	prefix := ""
	if rel, err := filepath.Rel(info.toplevel, root); err == nil && rel != "." {
		prefix = filepath.ToSlash(rel) + "/"
	}
	return func(p string) (string, bool) {
		if prefix == "" {
			return p, true
		}
		if !strings.HasPrefix(p, prefix) {
			return "", false // outside the served subtree
		}
		return p[len(prefix):], true
	}
}

// gitStatusAgainst maps changed files relative to root against base.
// When base is "HEAD" or empty, it returns working-tree changes only.
// When base is an arbitrary commit or ref (such as a PR merge-base),
// it includes both files changed against base and working-tree changes.
func gitStatusAgainst(root, base string) map[string]string {
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
	key := repoRelKey(info, root)

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

	// If diff base is set and not HEAD, overlay git diff --name-status against base
	if base != "" && base != "HEAD" {
		if diffOut, err := exec.Command("git", "-C", root, "diff", "--name-status", "-z", base).Output(); err == nil {
			parts := strings.Split(string(diffOut), "\x00")
			for i := 0; i < len(parts); i++ {
				stStr := parts[i]
				if stStr == "" {
					continue
				}
				code := stStr[0]
				if code == 'R' || code == 'C' {
					// R<score> \0 <src> \0 <dst>
					i += 2
					if i < len(parts) {
						if k, ok := key(parts[i]); ok {
							if _, exists := status[k]; !exists {
								status[k] = string(code)
							}
						}
					}
				} else {
					i++
					if i < len(parts) {
						if k, ok := key(parts[i]); ok {
							if _, exists := status[k]; !exists {
								status[k] = string(code)
							}
						}
					}
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

// gitStagedPaths maps repo-relative-to-served-root path -> true for every
// file with staged (index) changes. Used to drive the stage tick in the file
// tree and to gate gitCommit. Fails quiet: nil on any error or no repo.
func gitStagedPaths(root string) map[string]bool {
	info := gitProbe(root)
	if !info.ok {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", "--cached", "-z").Output()
	if err != nil {
		return nil
	}
	key := repoRelKey(info, root)
	staged := map[string]bool{}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if k, ok := key(p); ok {
			staged[k] = true
		}
	}
	if len(staged) == 0 {
		return nil
	}
	return staged
}

// gitStagedFiles returns a list of repo-relative paths staged in the index.
func gitStagedFiles(root string) []string {
	info := gitProbe(root)
	if !info.ok {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "diff", "--name-only", "--cached", "-z").Output()
	if err != nil {
		return nil
	}
	key := repoRelKey(info, root)
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		if k, ok := key(p); ok {
			files = append(files, k)
		}
	}
	return files
}

// gitStagedStat returns the diffstat summary of staged changes against HEAD.
// Fails quiet -> "".
func gitStagedStat(root string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "diff", "--stat", "--cached").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

const (
	maxStagedDiffBytes = 32 * 1024 // 32 KB limit on staged diff to stay safely under ARG_MAX / MAX_ARG_STRLEN
)

var lockfileExclusions = []string{
	":(exclude)*package-lock.json",
	":(exclude)*yarn.lock",
	":(exclude)*pnpm-lock.yaml",
	":(exclude)*go.sum",
	":(exclude)*Cargo.lock",
	":(exclude)*composer.lock",
	":(exclude)*poetry.lock",
	":(exclude)*Gemfile.lock",
	":(exclude)*.min.js",
	":(exclude)*.min.css",
	":(exclude)*.map",
}

// gitStagedDiff returns the unified diff of the index (staged changes)
// against HEAD, for handing to a coding harness asked to write a commit
// message. Large diffs are capped to maxStagedDiffBytes and noise files
// (lockfiles, minified assets) are excluded when other changes exist.
// Fails quiet -> "".
func gitStagedDiff(root string) string {
	if !gitAvailable(root) {
		return ""
	}
	// Try fetching the diff excluding high-noise files (lockfiles, minified bundles)
	cmdArgs := append([]string{"-C", root, "diff", "--no-color", "--cached", "--", "."}, lockfileExclusions...)
	out, err := exec.Command("git", cmdArgs...).Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		// Fallback to unfiltered diff if excluded diff was empty or failed (e.g. only lockfiles were modified)
		out, err = exec.Command("git", "-C", root, "diff", "--no-color", "--cached").Output()
		if err != nil {
			return ""
		}
	}
	if len(out) > maxStagedDiffBytes {
		cut := maxStagedDiffBytes
		if idx := bytes.LastIndexByte(out[:cut], '\n'); idx > 0 {
			cut = idx
		}
		return string(out[:cut]) + "\n\n[Diff truncated: showing first 32KB. See changed files and summary above for full list of changes]"
	}
	return string(out)
}

// gitHasUncommittedChanges reports whether the working tree or index has any
// changes at all (staged, unstaged, or untracked). Used to gate commit and
// pull -- a pull is refused outright when there's anything uncommitted,
// rather than risking it colliding with incoming changes.
func gitHasUncommittedChanges(root string) bool {
	if !gitAvailable(root) {
		return false
	}
	out, err := exec.Command("git", "-C", root, "status", "--porcelain", "-uall").Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(out))) > 0
}

// gitStage adds relpath to the index. An empty relpath (the served root
// itself) means "stage everything", so a bare "Stage All" action can reuse
// this instead of a separate endpoint.
func gitStage(root, relpath string) error {
	if relpath == "" {
		relpath = "."
	}
	out, err := exec.Command("git", "-C", root, "add", "--", relpath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitUnstage removes relpath from the index without touching the working tree.
func gitUnstage(root, relpath string) error {
	out, err := exec.Command("git", "-C", root, "reset", "--", relpath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git reset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitCommit commits whatever is currently staged. Refuses up front when
// nothing is staged so the caller gets a clear message instead of git's own
// "nothing to commit" noise.
func gitCommit(root, message string) error {
	if len(gitStagedPaths(root)) == 0 {
		return errors.New("nothing staged to commit")
	}
	out, err := exec.Command("git", "-C", root, "commit", "-m", message).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// gitCurrentBranch returns the checked-out branch name, or "HEAD" when
// detached (e.g. inside a PR review worktree).
func gitCurrentBranch(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// errNotFastForward is returned by gitFFOnlyPull when fetching ref would not
// fast-forward the current branch; resolving that is not supported.
var errNotFastForward = errors.New("not a fast-forward")

// gitFFOnlyPull fetches ref from remote and fast-forwards the current branch
// onto it. It never touches history any other way: if the merge would not be
// a clean fast-forward, it returns errNotFastForward without modifying
// anything, leaving conflict resolution to the user in a terminal.
func gitFFOnlyPull(root, remote, ref string) error {
	if out, err := exec.Command("git", "-C", root, "fetch", "--no-tags", remote, ref).CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := exec.Command("git", "-C", root, "merge", "--ff-only", "FETCH_HEAD").CombinedOutput(); err != nil {
		return errNotFastForward
	}
	return nil
}

// gitPush pushes the current branch to its configured remote. Returns
// trimmed combined output so the caller can recognize specific failures
// (e.g. no upstream configured) in the error text.
func gitPush(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "push").CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitPushSetUpstream pushes branch to remote and records it as the
// upstream, for a first push when the branch has none configured yet.
func gitPushSetUpstream(root, remote, branch string) (string, error) {
	out, err := exec.Command("git", "-C", root, "push", "-u", remote, branch).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitUpstream returns the remote and remote-branch name of branch's
// upstream (`@{u}`), or ok=false if none is configured.
func gitUpstream(root, branch string) (remote, remoteBranch string, ok bool) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", branch+"@{u}").Output()
	if err != nil {
		return "", "", false
	}
	remote, remoteBranch, found := strings.Cut(strings.TrimSpace(string(out)), "/")
	return remote, remoteBranch, found
}

// gitAheadBehind returns the number of commits HEAD is ahead and behind its
// upstream tracking branch (@{u}). If no upstream tracking branch is configured,
// it checks against origin/<branch> if available, or counts local commits if a
// remote is configured so initial publish pushes are permitted.
func gitAheadBehind(root string) (ahead, behind int, hasUpstream bool) {
	if !gitAvailable(root) {
		return 0, 0, false
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--left-right", "--count", "HEAD...@{u}").Output()
	if err == nil {
		parts := strings.Fields(string(out))
		if len(parts) >= 2 {
			a, _ := strconv.Atoi(parts[0])
			b, _ := strconv.Atoi(parts[1])
			return a, b, true
		}
	}

	branch := gitCurrentBranch(root)
	if branch != "" && branch != "HEAD" {
		if out, err := exec.Command("git", "-C", root, "rev-list", "--left-right", "--count", "HEAD...origin/"+branch).Output(); err == nil {
			parts := strings.Fields(string(out))
			if len(parts) >= 2 {
				a, _ := strconv.Atoi(parts[0])
				b, _ := strconv.Atoi(parts[1])
				return a, b, true
			}
		}

		if gitRemoteURL(root, "") != "" {
			if out, err := exec.Command("git", "-C", root, "rev-list", "--count", "HEAD").Output(); err == nil {
				c, _ := strconv.Atoi(strings.TrimSpace(string(out)))
				if c > 0 {
					return c, 0, false
				}
			}
		}
	}

	return 0, 0, false
}

// gitDiff returns the unified diff of relpath against HEAD. relpath is relative
// to the served root; git resolves it against -C root. Fails quiet -> "".
func gitDiff(root, relpath string) string {
	return gitDiffAgainst(root, relpath, "HEAD")
}

// gitDiffFull returns the whole unified diff for `git diff <refs...>`, capped at
// max bytes (with a marker line when cut). Untracked files are not part of a git
// diff; callers that care list them separately (gitUntracked).
func gitDiffFull(root string, max int, refs ...string) string {
	if !gitAvailable(root) {
		return ""
	}
	args := append([]string{"-C", root, "diff", "--no-color", "--no-ext-diff"}, refs...)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	if max > 0 && len(out) > max {
		return string(out[:max]) + "\n… diff truncated; run git diff for the rest …\n"
	}
	return string(out)
}

// gitUntracked lists untracked, non-ignored files relative to the served root.
func gitUntracked(root string) []string {
	info := gitProbe(root)
	if !info.ok {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil
	}
	var files []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			files = append(files, p)
		}
	}
	return files
}

// gitFilesBetween maps each path that differs between two commits to its
// name-status letter (A, M, D, R...), relative to the served root (paths outside
// it are dropped). PR review uses it for the PR's own file set and statuses:
// diffBase..head, fixed until the next Pull.
func gitFilesBetween(root, from, to string) map[string]string {
	info := gitProbe(root)
	if !info.ok || from == "" || to == "" {
		return nil
	}
	out, err := exec.Command("git", "-C", root, "diff", "--name-status", "-z", from, to).Output()
	if err != nil {
		return nil
	}
	key := repoRelKey(info, root)
	files := map[string]string{}
	parts := strings.Split(string(out), "\x00")
	for i := 0; i < len(parts); i++ {
		st := parts[i]
		if st == "" {
			continue
		}
		code := st[:1]
		if code == "R" || code == "C" {
			i += 2 // R<score> \0 <src> \0 <dst>
		} else {
			i++
		}
		if i < len(parts) {
			if k, ok := key(parts[i]); ok {
				files[k] = code
			}
		}
	}
	return files
}

// gitDiffAgainst is gitDiff generalized to an arbitrary base ref, so a PR
// review session (pr.go) can diff a file against the merge-base with the
// PR's target branch instead of the working tree's HEAD.
func gitDiffAgainst(root, relpath, base string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "diff", "--no-color", base, "--", relpath).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitDiffBetween is gitDiffAgainst generalized to a two-dot diff between two
// commits, rather than a commit against the working tree. A PR review
// session uses it to render the PR's own diff (merge-base..head) separately
// from the reviewer's local changes since checkout (head..working tree),
// which gitDiffAgainst covers.
func gitDiffBetween(root, relpath, from, to string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "diff", "--no-color", from, to, "--", relpath).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitMergeBase returns the merge-base commit of a and b, or "" if it cannot
// be determined (e.g. b was never fetched locally).
func gitMergeBase(root, a, b string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "merge-base", a, b).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitHunksAgainst parses the unified diff of relpath against base into
// 1-based NEW-FILE line numbers for a change gutter: added lines, modified
// (replaced) lines, and one marker per pure-deletion run (the new-file line
// immediately preceding the removed run; 0 means "before the first line").
// Fails quiet: empty when git is off/unavailable or the file has no diff
// against base (clean/untracked). base is "HEAD" for the working-tree
// gutter, or a PR's merge-base in review mode (server.go's diffBase).
func gitHunksAgainst(root, relpath, base string) (added, modified, deleted []int) {
	return parseDiffHunks(gitDiffAgainst(root, relpath, base))
}

// parseDiffHunks is gitHunksAgainst's body, split out so a commit's own diff
// (gitDiffCommit) can feed the same gutter parser as a diff against a ref.
func parseDiffHunks(diff string) (added, modified, deleted []int) {
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

// GitCommit represents a single commit in git log.
type GitCommit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

// gitRecentCommits returns up to count recent commits from HEAD.
func gitRecentCommits(root string, count int) []GitCommit {
	if count <= 0 {
		count = 5
	}
	out, err := exec.Command("git", "-C", root, "log", fmt.Sprintf("-n%d", count), "--format=%h%x1f%s%x1f%an%x1f%cr").Output()
	if err != nil {
		return []GitCommit{}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	commits := make([]GitCommit, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		c := GitCommit{
			Hash: parts[0],
		}
		if len(parts) > 1 {
			c.Subject = parts[1]
		}
		if len(parts) > 2 {
			c.Author = parts[2]
		}
		if len(parts) > 3 {
			c.Date = parts[3]
		}
		commits = append(commits, c)
	}
	return commits
}

/* ---------- unpushed commits (@{u}..HEAD) ----------

   Everything below reads commits that exist locally but not on the tracking
   branch, so they can be reviewed in px0 before they are pushed. All of it is
   strictly read-only and fails quiet: no upstream, no git, or a bad SHA all
   yield an empty result rather than an error, and the sidebar section simply
   doesn't appear. */

// UnpushedCommit is one commit in @{u}..HEAD, newest first.
type UnpushedCommit struct {
	Hash    string `json:"hash"`    // full SHA, what every follow-up call passes back
	Short   string `json:"short"`   // abbreviated SHA for display
	Subject string `json:"subject"` // first line of the message
	Author  string `json:"author"`
	Date    string `json:"date"` // relative ("2 hours ago")
}

// CommitFile is one path a commit touched, with git's name-status letter
// (M/A/D/R/C/T) -- the same alphabet the file tree badges working-tree changes
// with, so the commit's file list reads identically.
type CommitFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	From   string `json:"from,omitempty"` // previous path, for R/C only
}

// CommitDetail is everything the commit hover card shows: who wrote it, the
// full message, and its diffstat.
type CommitDetail struct {
	Hash       string `json:"hash"`
	Short      string `json:"short"`
	Subject    string `json:"subject"`
	Message    string `json:"message"` // full message, subject line included
	Author     string `json:"author"`
	Email      string `json:"email"`
	Date       string `json:"date"`    // relative
	DateISO    string `json:"dateIso"` // exact, for the title attribute
	Files      int    `json:"files"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	AuthorURL  string `json:"authorUrl,omitempty"` // best-effort GitHub profile/search link
	CommitURL  string `json:"commitUrl,omitempty"`
}

// shaRe bounds what reaches git as a revision. Every SHA the frontend sends
// came from a list px0 itself produced, so anything else is a bug or a probe;
// refusing it keeps a crafted "--upload-pack=..." out of an exec argument.
var shaRe = regexp.MustCompile(`^[0-9a-fA-F]{4,40}$`)

func validSHA(sha string) bool { return shaRe.MatchString(sha) }

// gitUpstreamRef returns the tracking branch HEAD is configured to push to, as
// git names it ("origin/master"), or "" when none is configured. Unlike
// gitAheadBehind there is no origin/<branch> fallback: an unpushed list is
// only meaningful against a branch git itself considers upstream, and guessing
// one would quietly measure against the wrong ref.
func gitUpstreamRef(root string) string {
	if !gitAvailable(root) {
		return ""
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitUnpushedCommits lists the commits in @{u}..HEAD, newest first, along with
// the upstream ref they are measured against. Both are empty when no upstream
// is configured or nothing is ahead.
func gitUnpushedCommits(root string, limit int) (upstream string, commits []UnpushedCommit) {
	upstream = gitUpstreamRef(root)
	if upstream == "" {
		return "", nil
	}
	return upstream, gitCommitsInRange(root, "@{u}..HEAD", limit)
}

// gitCommitsSince lists the commits in base..HEAD, newest first. PR review
// checkouts sit on a detached HEAD with no upstream, so the PR head they were
// checked out at (or last pushed to) stands in as the boundary.
func gitCommitsSince(root, base string, limit int) []UnpushedCommit {
	if !validSHA(base) {
		return nil
	}
	return gitCommitsInRange(root, base+"..HEAD", limit)
}

// gitCountSince returns how many commits HEAD has that base does not.
func gitCountSince(root, base string) int {
	if !validSHA(base) {
		return 0
	}
	out, err := exec.Command("git", "-C", root, "rev-list", "--count", base+"..HEAD").Output()
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	return n
}

func gitCommitsInRange(root, rng string, limit int) (commits []UnpushedCommit) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	out, err := exec.Command("git", "-C", root, "log", fmt.Sprintf("-n%d", limit),
		"--format=%H%x1f%h%x1f%s%x1f%an%x1f%cr", rng).Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\x1f")
		c := UnpushedCommit{Hash: f[0]}
		if len(f) > 1 {
			c.Short = f[1]
		}
		if len(f) > 2 {
			c.Subject = f[2]
		}
		if len(f) > 3 {
			c.Author = f[3]
		}
		if len(f) > 4 {
			c.Date = f[4]
		}
		commits = append(commits, c)
	}
	return commits
}

// commitDiffArgs are the diff-tree flags every per-commit read shares: the
// commit's own change against its first parent (--root so the very first
// commit still has one to show), one entry per path rather than per tree.
var commitDiffArgs = []string{"--no-commit-id", "-r", "-m", "--first-parent", "--root"}

// gitCommitFiles lists the paths a commit touched, relative to the served
// root. Paths outside root's subtree are dropped, the same way gitStatusAgainst
// drops them, so a repo served from a subdirectory lists only what it can open.
func gitCommitFiles(root, sha string) []CommitFile {
	info := gitProbe(root)
	if !info.ok || !validSHA(sha) {
		return nil
	}
	args := append([]string{"-C", root, "diff-tree", "--name-status", "-z"}, commitDiffArgs...)
	out, err := exec.Command("git", append(args, sha)...).Output()
	if err != nil {
		return nil
	}
	key := repoRelKey(info, root)
	var files []CommitFile
	// -z name-status emits "<status>\0<path>\0", or "<status>\0<src>\0<dst>\0"
	// for a rename or copy, so the field count per record varies.
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		st := fields[i]
		if st == "" {
			continue
		}
		letter := st[:1]
		rename := letter == "R" || letter == "C"
		if i+1 >= len(fields) {
			break
		}
		from, to := "", fields[i+1]
		i++
		if rename {
			if i+1 >= len(fields) {
				break
			}
			from, to = to, fields[i+1]
			i++
		}
		path, ok := key(to)
		if !ok {
			continue
		}
		f := CommitFile{Path: path, Status: letter}
		if from != "" {
			if fp, ok := key(from); ok {
				f.From = fp
			}
		}
		files = append(files, f)
	}
	return files
}

// gitCommitDetail reads a commit's metadata and diffstat for the hover card.
// ok is false for an unknown or malformed SHA.
func gitCommitDetail(root, sha string) (CommitDetail, bool) {
	if !gitAvailable(root) || !validSHA(sha) {
		return CommitDetail{}, false
	}
	// %B is last: it spans lines, so everything past the final separator is it.
	out, err := exec.Command("git", "-C", root, "show", "-s",
		"--format=%H%x1f%h%x1f%an%x1f%ae%x1f%cr%x1f%cI%x1f%s%x1f%B", sha).Output()
	if err != nil {
		return CommitDetail{}, false
	}
	f := strings.SplitN(string(out), "\x1f", 8)
	if len(f) < 8 {
		return CommitDetail{}, false
	}
	d := CommitDetail{
		Hash: f[0], Short: f[1], Author: f[2], Email: f[3],
		Date: f[4], DateISO: f[5], Subject: f[6],
		Message: strings.TrimRight(f[7], "\n"),
	}
	d.Files, d.Insertions, d.Deletions = gitCommitStat(root, sha)
	d.AuthorURL = githubAuthorURL(root, d.Email, d.Author)
	d.CommitURL = gitCommitWebURL(root, d.Hash)
	return d, true
}

// gitCommitStat sums a commit's numstat into files/insertions/deletions.
// Binary files count toward files but contribute no line counts, which is what
// git's own "--shortstat" does.
func gitCommitStat(root, sha string) (files, insertions, deletions int) {
	if !validSHA(sha) {
		return 0, 0, 0
	}
	args := append([]string{"-C", root, "diff-tree", "--numstat"}, commitDiffArgs...)
	out, err := exec.Command("git", append(args, sha)...).Output()
	if err != nil {
		return 0, 0, 0
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		files++
		if add, err := strconv.Atoi(parts[0]); err == nil { // "-" for binary
			insertions += add
		}
		if del, err := strconv.Atoi(parts[1]); err == nil {
			deletions += del
		}
	}
	return files, insertions, deletions
}

// gitDiffCommit returns the unified diff a single commit made to relpath --
// the commit against its first parent, or against the empty tree for a root
// commit. This is the ref form of gitDiffAgainst: the working tree isn't
// involved at all, so the result is frozen no matter what is edited since.
func gitDiffCommit(root, relpath, sha string) string {
	if !gitAvailable(root) || !validSHA(sha) {
		return ""
	}
	args := append([]string{"-C", root, "diff-tree", "-p", "--no-color"}, commitDiffArgs...)
	args = append(args, sha, "--", relpath)
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// gitHunksCommit is gitHunksAgainst for a single commit's own diff.
func gitHunksCommit(root, relpath, sha string) (added, modified, deleted []int) {
	return parseDiffHunks(gitDiffCommit(root, relpath, sha))
}

// remoteHostPath splits a remote URL -- in any of git's spellings -- into its
// host and "owner/repo" path. ok is false for a local path or anything it
// can't recognise.
func remoteHostPath(raw string) (host, path string, ok bool) {
	raw = strings.TrimSuffix(raw, ".git")
	switch {
	case strings.HasPrefix(raw, "git@"): // git@host:owner/repo
		h, p, found := strings.Cut(raw[4:], ":")
		if !found {
			return "", "", false
		}
		return h, strings.TrimPrefix(p, "/"), true
	case strings.HasPrefix(raw, "ssh://"): // ssh://git@host/owner/repo
		clean := strings.TrimPrefix(raw, "ssh://")
		if i := strings.Index(clean, "@"); i >= 0 {
			clean = clean[i+1:]
		}
		h, p, found := strings.Cut(clean, "/")
		if !found {
			return "", "", false
		}
		return h, p, true
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		if err != nil {
			return "", "", false
		}
		u.User = nil // strip user:token if any
		return u.Host, strings.TrimPrefix(u.Path, "/"), true
	}
	return "", "", false
}

// gitCommitWebURL returns a web URL for one commit on GitHub/GitLab/Bitbucket,
// or "" when the remote isn't one of those.
func gitCommitWebURL(root, sha string) string {
	host, path, ok := remoteHostPath(gitRemoteURL(root, gitCurrentBranch(root)))
	if !ok || path == "" {
		return ""
	}
	if strings.Contains(host, "gitlab") {
		return fmt.Sprintf("https://%s/%s/-/commit/%s", host, path, sha)
	}
	return fmt.Sprintf("https://%s/%s/commit/%s", host, path, sha)
}

// githubAuthorURL guesses a GitHub page for a commit author. A
// users.noreply.github.com address carries the login outright, so that becomes
// a profile link; otherwise the best that can be said from a commit alone is
// "this repo's commits by this address", which is at least always right about
// who it means. Non-GitHub remotes get nothing.
func githubAuthorURL(root, email, name string) string {
	host, path, ok := remoteHostPath(gitRemoteURL(root, gitCurrentBranch(root)))
	if !ok || path == "" || !strings.Contains(host, "github") {
		return ""
	}
	if login, _, found := strings.Cut(email, "@"); found && strings.HasSuffix(email, "users.noreply.github.com") {
		// "12345+octocat@users.noreply.github.com" and the older "octocat@..."
		if _, after, hasID := strings.Cut(login, "+"); hasID {
			login = after
		}
		if login != "" {
			return "https://" + host + "/" + url.PathEscape(login)
		}
	}
	q := email
	if q == "" {
		q = name
	}
	if q == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/%s/commits?author=%s", host, path, url.QueryEscape(q))
}

// gitHeadCommit returns the abbreviated or full HEAD commit hash.
func gitHeadCommit(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitRemoteURL returns the fetch URL for the repo's upstream or origin remote.
func gitRemoteURL(root, branch string) string {
	if branch != "" {
		if remote, _, ok := gitUpstream(root, branch); ok {
			if out, err := exec.Command("git", "-C", root, "config", "--get", fmt.Sprintf("remote.%s.url", remote)).Output(); err == nil {
				if u := strings.TrimSpace(string(out)); u != "" {
					return u
				}
			}
		}
	}
	out, err := exec.Command("git", "-C", root, "config", "--get", "remote.origin.url").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

// gitCommitsWebURL returns a web URL to view commits on GitHub/GitLab/Bitbucket if configured.
func gitCommitsWebURL(root, branch string) string {
	host, path, ok := remoteHostPath(gitRemoteURL(root, branch))
	if !ok || path == "" {
		return ""
	}
	seg := "commits"
	if strings.Contains(host, "gitlab") {
		seg = "-/commits"
	}
	if branch != "" && branch != "HEAD" {
		return fmt.Sprintf("https://%s/%s/%s/%s", host, path, seg, branch)
	}
	return fmt.Sprintf("https://%s/%s/%s", host, path, seg)
}

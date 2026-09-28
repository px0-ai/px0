package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
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
	diff := gitDiffAgainst(root, relpath, base)
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
	raw := gitRemoteURL(root, branch)
	if raw == "" {
		return ""
	}
	raw = strings.TrimSuffix(raw, ".git")

	// git@host:owner/repo
	if strings.HasPrefix(raw, "git@") {
		parts := strings.SplitN(raw[4:], ":", 2)
		if len(parts) == 2 {
			host := parts[0]
			path := strings.TrimPrefix(parts[1], "/")
			if strings.Contains(host, "gitlab") {
				if branch != "" && branch != "HEAD" {
					return fmt.Sprintf("https://%s/%s/-/commits/%s", host, path, branch)
				}
				return fmt.Sprintf("https://%s/%s/-/commits", host, path)
			}
			if branch != "" && branch != "HEAD" {
				return fmt.Sprintf("https://%s/%s/commits/%s", host, path, branch)
			}
			return fmt.Sprintf("https://%s/%s/commits", host, path)
		}
	}

	// ssh://git@host/owner/repo
	if strings.HasPrefix(raw, "ssh://") {
		clean := strings.TrimPrefix(raw, "ssh://")
		if idx := strings.Index(clean, "@"); idx >= 0 {
			clean = clean[idx+1:]
		}
		parts := strings.SplitN(clean, "/", 2)
		if len(parts) == 2 {
			host := parts[0]
			path := parts[1]
			if strings.Contains(host, "gitlab") {
				if branch != "" && branch != "HEAD" {
					return fmt.Sprintf("https://%s/%s/-/commits/%s", host, path, branch)
				}
				return fmt.Sprintf("https://%s/%s/-/commits", host, path)
			}
			if branch != "" && branch != "HEAD" {
				return fmt.Sprintf("https://%s/%s/commits/%s", host, path, branch)
			}
			return fmt.Sprintf("https://%s/%s/commits", host, path)
		}
	}

	// https:// or http://
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err == nil {
			u.User = nil // Strip user:token if any
			path := strings.TrimPrefix(strings.TrimSuffix(u.Path, ".git"), "/")
			if strings.Contains(u.Host, "gitlab") {
				if branch != "" && branch != "HEAD" {
					return fmt.Sprintf("https://%s/%s/-/commits/%s", u.Host, path, branch)
				}
				return fmt.Sprintf("https://%s/%s/-/commits", u.Host, path)
			}
			if branch != "" && branch != "HEAD" {
				return fmt.Sprintf("https://%s/%s/commits/%s", u.Host, path, branch)
			}
			return fmt.Sprintf("https://%s/%s/commits", u.Host, path)
		}
	}

	return ""
}

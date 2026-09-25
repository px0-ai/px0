package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileEntry represents a single indexed file in the workspace,
// storing its relative path, name, size, and pre-computed lowercase representation
// for fast fuzzy searching.
type FileEntry struct {
	Path      string `json:"path"` // Slash-separated path relative to workspace root
	Name      string `json:"name"` // Basename of the file
	Size      int64  `json:"size"` // File size in bytes
	lower     string // Cached lowercase Path for fast case-insensitive matching
	nameStart int    // Byte offset in Path where the basename begins
}

// Node represents a file or directory entry in the hierarchical file tree view.
type Node struct {
	Name       string `json:"name"`                 // File or directory name
	Path       string `json:"path"`                 // Slash-separated path relative to workspace root
	Dir        bool   `json:"dir"`                  // True if this node is a directory
	Size       int64  `json:"size"`                 // File size in bytes (0 for directories)
	Ignored    bool   `json:"ignored,omitempty"`    // Matched by .gitignore: listed in tree dimmed, never indexed
	Status     string `json:"status,omitempty"`     // Git working-tree status code: M (modified), A (added), D (deleted), U (untracked), etc.
	Staged     bool   `json:"staged,omitempty"`     // True if the file has staged changes in the git index
	Dirty      bool   `json:"dirty,omitempty"`      // For folders: true if any descendant has git changes
	YourStatus string `json:"yourStatus,omitempty"` // PR review mode: reviewer's local changes since checkout
	YourDirty  bool   `json:"yourDirty,omitempty"`  // PR review mode for folders: contains reviewer-changed descendant
}

// vcsDirs are version control internals. Unlike other ignored entries they are
// not even listed: nobody reads them, and .git is present in nearly every repo.
var vcsDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

// Index maintains in-memory representation of the workspace filesystem:
// a flat list of all unignored files for fast searching, a directory tree map
// for lazy sidebar rendering, and live git status tracking.
type Index struct {
	root string

	mu               sync.RWMutex
	files            []FileEntry
	children         map[string][]Node
	builtAt          time.Time
	buildMS          int64
	gitChanges       int
	gitFiles         []string
	gitStatusMap     map[string]string
	gitStagedMap     map[string]bool
	gitYourStatusMap map[string]string
	diffBase         string
	prHead           string
	readyCh          chan struct{}
}

// NewIndex constructs a new unpopulated workspace index for the given root directory.
func NewIndex(root string) *Index {
	return &Index{root: root, children: map[string][]Node{}, readyCh: make(chan struct{})}
}

// Root returns the absolute path to the workspace root directory.
func (ix *Index) Root() string { return ix.root }

// SetDiffBase sets the git ref or commit against which working tree diffs and statuses are computed.
func (ix *Index) SetDiffBase(base string) {
	ix.mu.Lock()
	ix.diffBase = base
	ix.mu.Unlock()
}

// DiffBase returns the current git diff base ref (e.g. "HEAD" or PR merge-base).
func (ix *Index) DiffBase() string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.diffBase
}

// SetPRHead stores the checked-out PR head commit SHA in PR review mode.
func (ix *Index) SetPRHead(head string) {
	ix.mu.Lock()
	ix.prHead = head
	ix.mu.Unlock()
}

// PRHead returns the checked-out PR head commit SHA, or empty if not in PR review mode.
func (ix *Index) PRHead() string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.prHead
}

// Ready reports whether the initial index build has finished.
func (ix *Index) Ready() bool {
	select {
	case <-ix.readyCh:
		return true
	default:
		return false
	}
}

// WaitReady blocks until the initial index build completes or ctx is canceled.
func (ix *Index) WaitReady(ctx context.Context) error {
	select {
	case <-ix.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns the total indexed file count, the timestamp of the build, and duration in milliseconds.
func (ix *Index) Stats() (files int, builtAt time.Time, ms int64) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.files), ix.builtAt, ix.buildMS
}

// Files returns a copy or view of all indexed file entries.
func (ix *Index) Files() []FileEntry {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.files
}

// GitChanges returns the number of modified files and their workspace-relative paths.
func (ix *Index) GitChanges() (int, []string) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	res := make([]string, len(ix.gitFiles))
	copy(res, ix.gitFiles)
	return ix.gitChanges, res
}

// GitStatusMap returns a copy of the mapping from relative path to git status letter.
func (ix *Index) GitStatusMap() map[string]string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.gitStatusMap == nil {
		return map[string]string{}
	}
	res := make(map[string]string, len(ix.gitStatusMap))
	for k, v := range ix.gitStatusMap {
		res[k] = v
	}
	return res
}

// GitStagedMap returns a copy of the mapping from relative path to staged boolean flag.
func (ix *Index) GitStagedMap() map[string]bool {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.gitStagedMap == nil {
		return map[string]bool{}
	}
	res := make(map[string]bool, len(ix.gitStagedMap))
	for k, v := range ix.gitStagedMap {
		res[k] = v
	}
	return res
}

// GitYourStatusMap returns a copy of the reviewer local modifications map in PR mode.
func (ix *Index) GitYourStatusMap() map[string]string {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	if ix.gitYourStatusMap == nil {
		return map[string]string{}
	}
	res := make(map[string]string, len(ix.gitYourStatusMap))
	for k, v := range ix.gitYourStatusMap {
		res[k] = v
	}
	return res
}

// Children lists a directory for the tree. Ignored directories are never walked,
// so their contents are read from disk on demand, all marked ignored: git cannot
// re-include anything beneath an excluded directory either.
func (ix *Index) Children(dir string) ([]Node, bool) {
	ix.mu.RLock()
	c, ok := ix.children[dir]
	under := !ok && ix.underIgnoredLocked(dir)
	var res []Node
	if ok {
		res = make([]Node, len(c))
		copy(res, c)
	}
	ix.mu.RUnlock()
	if ok || !under {
		return res, ok
	}
	return ix.listIgnored(dir)
}

// underIgnoredLocked reports whether dir sits inside a directory the walk
// listed as ignored. The caller holds ix.mu.
func (ix *Index) underIgnoredLocked(dir string) bool {
	if dir == "" || strings.ContainsRune(dir, '\\') {
		return false
	}
	for _, seg := range strings.Split(dir, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false // never let a crafted path climb out of the root
		}
	}
	// The nearest ancestor the walk visited decides: its entry for the next
	// segment down must be an ignored directory.
	for p := dir; p != ""; {
		parent := ""
		if i := strings.LastIndexByte(p, '/'); i >= 0 {
			parent = p[:i]
		}
		if kids, ok := ix.children[parent]; ok {
			name := strings.TrimPrefix(p[len(parent):], "/")
			for _, k := range kids {
				if k.Name == name {
					return k.Dir && k.Ignored
				}
			}
			return false
		}
		p = parent
	}
	return false
}

// listIgnored reads the immediate entries of an ignored directory directly from disk on demand,
// marking all child nodes as Ignored. This allows the file tree UI to expand ignored directories
// without needing them to be walked or kept in the search index.
func (ix *Index) listIgnored(dir string) ([]Node, bool) {
	ents, err := os.ReadDir(filepath.Join(ix.root, filepath.FromSlash(dir)))
	if err != nil {
		return nil, false
	}
	kids := make([]Node, 0, len(ents))
	for _, e := range ents {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		kids = append(kids, Node{Name: e.Name(), Path: dir + "/" + e.Name(), Dir: e.IsDir(), Ignored: true})
	}
	sortNodes(kids)
	return kids, true
}

// sortNodes orders a listing directories first, then case-insensitively by name.
func sortNodes(kids []Node) {
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].Dir != kids[j].Dir {
			return kids[i].Dir
		}
		return strings.ToLower(kids[i].Name) < strings.ToLower(kids[j].Name)
	})
}

// Build walks the tree once, honouring .gitignore at every level, and
// materialises both the flat file list (for fuzzy find and search) and the
// directory map (for the tree view). Root entries are published immediately so
// the frontend can display the file tree without waiting for the full repo scan.
func (ix *Index) Build() {
	start := time.Now()
	root := newIgnoreSet(nil)
	root = root.child(readGitignore(ix.root, ""))

	// Git status is computed up front, before the walk, rather than
	// concurrently with it. It used to run in a goroutine so its ~80ms
	// overlapped the tree scan, but the overlay was then only applied once,
	// in a second pass after the *entire* walk finished -- so the root
	// directory's early publish below (meant to show the tree instantly)
	// carried no Dirty/Status info until the whole repo had been walked,
	// which on a large repo could be seconds later. Doing it first means
	// every node -- including the immediate root snapshot -- is published
	// with correct git status from the start.
	base := ix.DiffBase()
	gs := gitStatusAgainst(ix.root, base)
	staged := gitStagedPaths(ix.root)
	dirtyDirs := map[string]bool{}
	var gitFiles []string
	if gs != nil {
		for p := range gs {
			gitFiles = append(gitFiles, p)
			for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
				p = p[:i]
				dirtyDirs[p] = true
			}
		}
		sort.Slice(gitFiles, func(i, j int) bool {
			si, sj := gs[gitFiles[i]], gs[gitFiles[j]]
			if (si != "U") != (sj != "U") {
				return si != "U"
			}
			return gitFiles[i] < gitFiles[j]
		})
	}

	head := ix.PRHead()
	var yourStatuses map[string]string
	yourDirtyDirs := map[string]bool{}
	if head != "" {
		yourStatuses = gitStatusAgainst(ix.root, head)
		for p := range yourStatuses {
			for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
				p = p[:i]
				yourDirtyDirs[p] = true
			}
		}
	}

	var (
		mu       sync.Mutex
		files    []FileEntry
		children = map[string][]Node{}
		wg       sync.WaitGroup
		sem      = make(chan struct{}, runtime.NumCPU()*4)
	)

	var walk func(abs, rel string, ig *ignoreSet)
	walk = func(abs, rel string, ig *ignoreSet) {
		defer wg.Done()
		ents, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		if rel != "" {
			if extra := readGitignore(abs, rel); len(extra) > 0 {
				ig = ig.child(extra)
			}
		}
		kids := make([]Node, 0, len(ents))
		var subdirs []struct {
			abs, rel string
		}
		for _, e := range ents {
			name := e.Name()
			childRel := name
			if rel != "" {
				childRel = rel + "/" + name
			}
			isDir := e.IsDir()
			// Follow nothing through symlinks; cycles are not worth the risk.
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			if ig.match(childRel, isDir) {
				// Listed so the tree can show it dimmed, but never walked or
				// indexed, so search and quick open stay out of it.
				if !(isDir && vcsDirs[name]) {
					kids = append(kids, Node{Name: name, Path: childRel, Dir: isDir, Ignored: true})
				}
				continue
			}
			if isDir {
				kids = append(kids, Node{Name: name, Path: childRel, Dir: true, Dirty: dirtyDirs[childRel], YourDirty: yourDirtyDirs[childRel]})
				subdirs = append(subdirs, struct{ abs, rel string }{filepath.Join(abs, name), childRel})
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			kids = append(kids, Node{Name: name, Path: childRel, Size: info.Size(), Status: gs[childRel], Staged: staged[childRel], YourStatus: yourStatuses[childRel]})
			mu.Lock()
			files = append(files, FileEntry{
				Path: childRel, Name: name, Size: info.Size(),
				lower: strings.ToLower(childRel), nameStart: len(childRel) - len(name),
			})
			mu.Unlock()
		}
		sortNodes(kids)
		mu.Lock()
		children[rel] = kids
		mu.Unlock()

		// If this is the root directory, make it available to ix.Children("")
		// immediately so the browser UI can render the sidebar tree without delay.
		if rel == "" {
			ix.mu.Lock()
			if ix.children == nil {
				ix.children = map[string][]Node{}
			}
			ix.children[""] = kids
			ix.mu.Unlock()
		}

		for _, sd := range subdirs {
			wg.Add(1)
			select {
			case sem <- struct{}{}:
				go func(a, r string, g *ignoreSet) {
					defer func() { <-sem }()
					walk(a, r, g)
				}(sd.abs, sd.rel, ig)
			default:
				walk(sd.abs, sd.rel, ig) // pool saturated: recurse inline
			}
		}
	}

	wg.Add(1)
	walk(ix.root, "", root)
	wg.Wait()

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })

	ix.mu.Lock()
	ix.gitChanges = len(gitFiles)
	ix.gitFiles = gitFiles
	ix.gitStatusMap = gs
	ix.gitStagedMap = staged
	ix.gitYourStatusMap = yourStatuses
	ix.files, ix.children = files, children
	ix.builtAt, ix.buildMS = time.Now(), time.Since(start).Milliseconds()
	select {
	case <-ix.readyCh:
	default:
		close(ix.readyCh)
	}
	ix.mu.Unlock()
}

// UpdateGitStatus re-runs git status, updates in-memory status codes, staged
// flags, and dirty directory markers across ix.children without re-walking
// the filesystem tree. Reports gitChanges count, gitFiles list, whether
// anything changed (status, staged, or yourStatus), and the raw status/staged/dirty-dir
// maps.
func (ix *Index) UpdateGitStatus() (count int, files []string, changed bool, statuses map[string]string, dirtyDirs map[string]bool, staged map[string]bool, yourStatuses map[string]string, yourDirtyDirs map[string]bool) {
	if !ix.Ready() || gitDisabled || !gitAvailable(ix.root) {
		return 0, nil, false, nil, nil, nil, nil, nil
	}

	base := ix.DiffBase()
	gs := gitStatusAgainst(ix.root, base)
	if gs == nil {
		gs = map[string]string{}
	}
	sg := gitStagedPaths(ix.root)
	if sg == nil {
		sg = map[string]bool{}
	}

	head := ix.PRHead()
	var ys map[string]string
	newYourDirtyDirs := map[string]bool{}
	if head != "" {
		ys = gitStatusAgainst(ix.root, head)
		if ys == nil {
			ys = map[string]string{}
		}
		for p := range ys {
			for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
				p = p[:i]
				newYourDirtyDirs[p] = true
			}
		}
	}

	newDirtyDirs := map[string]bool{}
	var newGitFiles []string
	for p := range gs {
		newGitFiles = append(newGitFiles, p)
		for i := strings.LastIndexByte(p, '/'); i >= 0; i = strings.LastIndexByte(p, '/') {
			p = p[:i]
			newDirtyDirs[p] = true
		}
	}
	sort.Slice(newGitFiles, func(i, j int) bool {
		si, sj := gs[newGitFiles[i]], gs[newGitFiles[j]]
		if (si != "U") != (sj != "U") {
			return si != "U"
		}
		return newGitFiles[i] < newGitFiles[j]
	})

	ix.mu.Lock()
	defer ix.mu.Unlock()

	// Check if status, staged, and yourStatuses maps are all unchanged
	same := len(gs) == len(ix.gitStatusMap) && len(sg) == len(ix.gitStagedMap) && len(ys) == len(ix.gitYourStatusMap)
	if same {
		for k, v := range gs {
			if ix.gitStatusMap[k] != v {
				same = false
				break
			}
		}
	}
	if same {
		for k, v := range sg {
			if ix.gitStagedMap[k] != v {
				same = false
				break
			}
		}
	}
	if same {
		for k, v := range ys {
			if ix.gitYourStatusMap[k] != v {
				same = false
				break
			}
		}
	}
	if same {
		resFiles := make([]string, len(ix.gitFiles))
		copy(resFiles, ix.gitFiles)
		return ix.gitChanges, resFiles, false, gs, newDirtyDirs, sg, ys, newYourDirtyDirs
	}

	// Update nodes in-place across ix.children
	for _, kids := range ix.children {
		for i := range kids {
			if kids[i].Dir {
				kids[i].Dirty = newDirtyDirs[kids[i].Path]
				kids[i].YourDirty = newYourDirtyDirs[kids[i].Path]
			} else {
				kids[i].Status = gs[kids[i].Path]
				kids[i].Staged = sg[kids[i].Path]
				kids[i].YourStatus = ys[kids[i].Path]
			}
		}
	}

	ix.gitChanges = len(newGitFiles)
	ix.gitFiles = newGitFiles
	ix.gitStatusMap = gs
	ix.gitStagedMap = sg
	ix.gitYourStatusMap = ys

	resFiles := make([]string, len(ix.gitFiles))
	copy(resFiles, ix.gitFiles)
	return ix.gitChanges, resFiles, true, gs, newDirtyDirs, sg, ys, newYourDirtyDirs
}

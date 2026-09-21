package main

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

type FileEntry struct {
	Path      string `json:"path"` // slash-separated, relative to root
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	ModTime   int64  `json:"-"`
	lower     string // cached lowercase Path for matching
	nameStart int    // index in Path where the basename begins
}

type Node struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	Ignored bool   `json:"ignored,omitempty"` // matched by .gitignore: listed, never indexed
	Status  string `json:"status,omitempty"`  // git working-tree status: M/A/D/?/R/C/U
	Dirty   bool   `json:"dirty,omitempty"`   // folder: contains a git-changed descendant
}

// vcsDirs are version control internals. Unlike other ignored entries they are
// not even listed: nobody reads them, and .git is present in nearly every repo.
var vcsDirs = map[string]bool{".git": true, ".hg": true, ".svn": true}

type Index struct {
	root   string
	remote *remoteClient

	mu           sync.RWMutex
	files        []FileEntry
	allFiles     []FileEntry
	children     map[string][]Node
	builtAt      time.Time
	buildMS      int64
	gitChanges   int
	gitFiles     []string
	gitStatusMap map[string]string
	readyCh      chan struct{}
	buildErr     error
}

func NewIndex(root string) *Index {
	return &Index{root: root, children: map[string][]Node{}, readyCh: make(chan struct{})}
}

func NewRemoteIndex(t remoteTarget) *Index {
	c := newRemoteClient(t)
	return &Index{root: c.displayRoot(), remote: c, children: map[string][]Node{}, readyCh: make(chan struct{})}
}

func (ix *Index) Root() string { return ix.root }

func (ix *Index) Remote() bool { return ix.remote != nil }

func (ix *Index) Ready() bool {
	select {
	case <-ix.readyCh:
		return true
	default:
		return false
	}
}

func (ix *Index) WaitReady(ctx context.Context) error {
	select {
	case <-ix.readyCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (ix *Index) Stats() (files int, builtAt time.Time, ms int64) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.files), ix.builtAt, ix.buildMS
}

func (ix *Index) Files() []FileEntry {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.files
}

func (ix *Index) GitChanges() (int, []string) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	res := make([]string, len(ix.gitFiles))
	copy(res, ix.gitFiles)
	return ix.gitChanges, res
}

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

func (ix *Index) BuildError() error {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.buildErr
}

func (ix *Index) File(rel string) (FileEntry, bool) {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	files := ix.files
	if ix.remote != nil {
		clean, ok := remoteRel(rel)
		if !ok || clean == "" {
			return FileEntry{}, false
		}
		rel = clean
		if ix.allFiles != nil {
			files = ix.allFiles
		}
	} else {
		rel = filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
		if ix.allFiles != nil {
			files = ix.allFiles
		}
	}
	i := sort.Search(len(files), func(i int) bool { return files[i].Path >= rel })
	if i < len(files) && files[i].Path == rel {
		return files[i], true
	}
	return FileEntry{}, false
}

func (ix *Index) ReadFile(ctx context.Context, rel string) ([]byte, error) {
	return ix.ReadFileLimit(ctx, rel, 0)
}

func (ix *Index) ReadFileLimit(ctx context.Context, rel string, limit int64) ([]byte, error) {
	if ix.remote != nil {
		return ix.remote.readFileLimit(ctx, rel, limit)
	}
	return os.ReadFile(filepath.Join(ix.root, filepath.FromSlash(rel)))
}

func (ix *Index) spoolFile(ctx context.Context, rel string) (*os.File, func(), error) {
	if ix.remote != nil {
		return ix.remote.spoolFile(ctx, rel)
	}
	f, err := os.Open(filepath.Join(ix.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { _ = f.Close() }, nil
}

// Children lists a directory for the tree. Ignored directories are never walked,
// so their contents are read from disk on demand, all marked ignored: git cannot
// re-include anything beneath an excluded directory either.
func (ix *Index) Children(dir string) ([]Node, bool) {
	ix.mu.RLock()
	c, ok := ix.children[dir]
	under := !ok && ix.underIgnoredLocked(dir)
	ix.mu.RUnlock()
	if ok || !under {
		return c, ok
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
	if ix.remote != nil {
		ix.buildRemote()
		return
	}
	start := time.Now()
	root := newIgnoreSet(nil)
	root = root.child(readGitignore(ix.root, ""))

	var (
		mu       sync.Mutex
		files    []FileEntry
		children = map[string][]Node{}
		wg       sync.WaitGroup
		sem      = make(chan struct{}, runtime.NumCPU()*4)
	)

	// git status only needs the repo root, not the walk result, so run it
	// concurrently with the walk instead of serially after it — on large repos
	// the ~80ms subprocess overlaps the tree scan rather than adding to it.
	gsCh := make(chan map[string]string, 1)
	go func() { gsCh <- gitStatus(ix.root) }()

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
				kids = append(kids, Node{Name: name, Path: childRel, Dir: true})
				subdirs = append(subdirs, struct{ abs, rel string }{filepath.Join(abs, name), childRel})
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			kids = append(kids, Node{Name: name, Path: childRel, Size: info.Size()})
			mu.Lock()
			files = append(files, FileEntry{
				Path: childRel, Name: name, Size: info.Size(),
				ModTime: info.ModTime().UnixNano(),
				lower:   strings.ToLower(childRel), nameStart: len(childRel) - len(name),
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

	// Overlay git working-tree status onto file nodes (computed concurrently with
	// the walk above); nil when git is unavailable or off.
	gs := <-gsCh

	// Every ancestor directory of a changed file is dirty, so a collapsed folder
	// can badge without the frontend fetching its subtree.
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

	ix.mu.Lock()
	if gs != nil {
		for _, kids := range children {
			for i := range kids {
				if kids[i].Dir {
					kids[i].Dirty = dirtyDirs[kids[i].Path]
				} else if code, ok := gs[kids[i].Path]; ok {
					kids[i].Status = code
				}
			}
		}
	}
	ix.gitChanges = len(gitFiles)
	ix.gitFiles = gitFiles
	ix.gitStatusMap = gs
	ix.files, ix.allFiles, ix.children, ix.buildErr = files, files, children, nil
	ix.builtAt, ix.buildMS = time.Now(), time.Since(start).Milliseconds()
	select {
	case <-ix.readyCh:
	default:
		close(ix.readyCh)
	}
	ix.mu.Unlock()
}

func (ix *Index) buildRemote() {
	start := time.Now()
	ctx := context.Background()
	filesRaw, err := ix.remote.listFiles(ctx)
	if err != nil {
		ix.mu.Lock()
		ix.files = nil
		ix.allFiles = nil
		ix.children = map[string][]Node{}
		ix.buildErr = err
		ix.builtAt, ix.buildMS = time.Now(), time.Since(start).Milliseconds()
		select {
		case <-ix.readyCh:
		default:
			close(ix.readyCh)
		}
		ix.mu.Unlock()
		return
	}
	sort.Slice(filesRaw, func(i, j int) bool {
		if filesRaw[i].Dir != filesRaw[j].Dir {
			return filesRaw[i].Dir
		}
		return filesRaw[i].Path < filesRaw[j].Path
	})
	children := map[string][]Node{}
	seen := map[string]map[string]bool{}
	ensureDir := func(dir string) {
		if _, ok := children[dir]; !ok {
			children[dir] = []Node{}
		}
		if _, ok := seen[dir]; !ok {
			seen[dir] = map[string]bool{}
		}
	}
	ensureDir("")
	var files []FileEntry
	var allFiles []FileEntry
	addNode := func(rel string, dir bool, size int64, ignored bool) {
		parts := strings.Split(rel, "/")
		parent := ""
		for i, part := range parts {
			ensureDir(parent)
			if seen[parent][part] {
				if i < len(parts)-1 {
					parent = strings.Trim(parent+"/"+part, "/")
				}
				continue
			}
			child := strings.Trim(parent+"/"+part, "/")
			node := Node{Name: part, Path: child, Dir: i < len(parts)-1 || dir}
			if ignored {
				node.Ignored = true
			}
			if !node.Dir {
				node.Size = size
			}
			children[parent] = append(children[parent], node)
			seen[parent][part] = true
			if node.Dir {
				ensureDir(child)
				parent = child
			}
		}
	}
	fileEntry := func(rf remoteFile) FileEntry {
		return FileEntry{
			Path: rf.Path, Name: rf.Name, Size: rf.Size, ModTime: rf.ModTime,
			lower: strings.ToLower(rf.Path), nameStart: len(rf.Path) - len(rf.Name),
		}
	}
	dirSet := map[string]bool{"": true}
	gitignoreFiles := map[string]bool{}
	for _, rf := range filesRaw {
		if rf.Dir {
			dirSet[rf.Path] = true
		} else if rf.Path == ".gitignore" || strings.HasSuffix(rf.Path, "/.gitignore") {
			gitignoreFiles[rf.Path] = true
		}
		for p := path.Dir(rf.Path); p != "." && p != "/"; p = path.Dir(p) {
			dirSet[p] = true
		}
	}
	ignoreCache := map[string]*ignoreSet{}
	var ignoreFor func(string) *ignoreSet
	ignoreFor = func(dir string) *ignoreSet {
		if ig, ok := ignoreCache[dir]; ok {
			return ig
		}
		var parent *ignoreSet
		if dir == "" {
			parent = newIgnoreSet(nil)
		} else {
			p := path.Dir(dir)
			if p == "." {
				p = ""
			}
			parent = ignoreFor(p)
		}
		gitignore := ".gitignore"
		if dir != "" {
			gitignore = dir + "/.gitignore"
		}
		ig := parent
		if gitignoreFiles[gitignore] {
			if data, err := ix.remote.readFileLimit(ctx, gitignore, 1<<20); err == nil {
				ig = parent.child(parseGitignore(bytes.NewReader(data), dir))
			}
		}
		ignoreCache[dir] = ig
		return ig
	}
	ignoredDirs := map[string]bool{}
	ignoredAncestor := func(rel string) bool {
		for p := path.Dir(rel); p != "." && p != "/"; p = path.Dir(p) {
			if ignoredDirs[p] {
				return true
			}
		}
		return false
	}
	for _, rf := range filesRaw {
		first := rf.Path
		if i := strings.IndexByte(first, '/'); i >= 0 {
			first = first[:i]
		}
		if vcsDirs[first] {
			if rf.Dir {
				ignoredDirs[rf.Path] = true
			}
			continue
		}
		if ignoredAncestor(rf.Path) {
			addNode(rf.Path, rf.Dir, rf.Size, true)
			if !rf.Dir {
				allFiles = append(allFiles, fileEntry(rf))
			}
			continue
		}
		parent := path.Dir(rf.Path)
		if parent == "." {
			parent = ""
		}
		if !dirSet[parent] {
			parent = ""
		}
		ignored := ignoreFor(parent).match(rf.Path, rf.Dir)
		if ignored {
			if rf.Dir {
				ignoredDirs[rf.Path] = true
			}
			name := rf.Name
			if name == "" {
				name = rf.Path[strings.LastIndexByte(rf.Path, '/')+1:]
			}
			if !(rf.Dir && vcsDirs[name]) {
				addNode(rf.Path, rf.Dir, rf.Size, true)
				if !rf.Dir {
					allFiles = append(allFiles, fileEntry(rf))
				}
			}
			continue
		}
		if rf.Dir {
			addNode(rf.Path, true, 0, false)
			continue
		}
		entry := fileEntry(rf)
		files = append(files, entry)
		allFiles = append(allFiles, entry)
		addNode(rf.Path, false, rf.Size, false)
	}
	for dir := range children {
		sortNodes(children[dir])
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sort.Slice(allFiles, func(i, j int) bool { return allFiles[i].Path < allFiles[j].Path })
	ix.mu.Lock()
	ix.files, ix.allFiles, ix.children, ix.buildErr = files, allFiles, children, nil
	ix.builtAt, ix.buildMS = time.Now(), time.Since(start).Milliseconds()
	select {
	case <-ix.readyCh:
	default:
		close(ix.readyCh)
	}
	ix.mu.Unlock()
}

// UpdateGitStatus re-runs git status, updates in-memory status codes and dirty
// directory markers across ix.children without re-walking the filesystem tree.
// Reports gitChanges count, gitFiles list, whether any status changed, and the
// raw status and dirty directory maps.
func (ix *Index) UpdateGitStatus() (count int, files []string, changed bool, statuses map[string]string, dirtyDirs map[string]bool) {
	if !ix.Ready() || gitDisabled || !gitAvailable(ix.root) {
		return 0, nil, false, nil, nil
	}

	gs := gitStatus(ix.root)
	if gs == nil {
		gs = map[string]string{}
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

	// Check if status map is unchanged
	same := len(gs) == len(ix.gitStatusMap)
	if same {
		for k, v := range gs {
			if ix.gitStatusMap[k] != v {
				same = false
				break
			}
		}
	}
	if same {
		resFiles := make([]string, len(ix.gitFiles))
		copy(resFiles, ix.gitFiles)
		return ix.gitChanges, resFiles, false, gs, newDirtyDirs
	}

	// Update nodes in-place across ix.children
	for _, kids := range ix.children {
		for i := range kids {
			if kids[i].Dir {
				kids[i].Dirty = newDirtyDirs[kids[i].Path]
			} else {
				kids[i].Status = gs[kids[i].Path]
			}
		}
	}

	ix.gitChanges = len(newGitFiles)
	ix.gitFiles = newGitFiles
	ix.gitStatusMap = gs

	resFiles := make([]string, len(ix.gitFiles))
	copy(resFiles, ix.gitFiles)
	return ix.gitChanges, resFiles, true, gs, newDirtyDirs
}

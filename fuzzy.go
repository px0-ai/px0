package main

import (
	"cmp"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// FuzzyResult represents a matched file path ranked by the fuzzy search engine.
type FuzzyResult struct {
	Path  string `json:"path"` // Workspace-relative path to the matched file
	Name  string `json:"name"` // Basename of the file
	Pos   []int  `json:"pos"`  // Byte offsets in Path that matched, used by frontend for highlight badges
	score int    // Computed match quality score (higher is better)
}

// isBoundary reports whether a byte acts as a word or segment boundary
// in file paths (e.g. slashes, underscores, dashes, dots, spaces, or at-symbols).
func isBoundary(b byte) bool {
	switch b {
	case '/', '_', '-', '.', ' ', '@':
		return true
	}
	return false
}

// fuzzyScore does a two-pass match: forward to prove every query rune is
// present, then backward from that endpoint to pull the matched positions as
// tightly together as possible. Tight matches score higher, which is what makes
// "fzf feel" work without an O(n*m) dynamic program.
func fuzzyScore(q, origQ string, e *FileEntry, pos []int) (int, []int, bool) {
	p, lp := e.Path, e.lower
	if len(q) > len(lp) {
		return 0, nil, false // query cannot fit inside the path
	}
	qi, end := 0, -1
	for i := 0; i < len(lp) && qi < len(q); i++ {
		if lp[i] == q[qi] {
			qi++
			end = i
		}
	}
	if qi < len(q) {
		return 0, nil, false
	}

	pos = pos[:0]
	qi = len(q) - 1
	for i := end; i >= 0 && qi >= 0; i-- {
		if lp[i] == q[qi] {
			pos = append(pos, i)
			qi--
		}
	}
	// Collected right-to-left; flip in place.
	for i, j := 0, len(pos)-1; i < j; i, j = i+1, j-1 {
		pos[i], pos[j] = pos[j], pos[i]
	}

	score, prev := 0, -2
	for k, i := range pos {
		if i == prev+1 {
			score += 12 // consecutive run
		} else if k > 0 {
			score -= min(i-prev, 12) // gap penalty, bounded
		}
		if i >= e.nameStart {
			score += 14 // basename beats directory noise
		}
		if i == 0 || isBoundary(p[i-1]) {
			score += 16 // start of a path or word segment
		} else if p[i] >= 'A' && p[i] <= 'Z' && p[i-1] >= 'a' && p[i-1] <= 'z' {
			score += 14 // camelCase hump
		}
		if k < len(origQ) && p[i] == origQ[k] {
			score += 4 // exact case
		}
		prev = i
	}
	// Prefer the shallower, shorter of two otherwise-equal paths.
	score -= len(p) / 8
	score -= e.depth * 2 // precomputed at index time; was strings.Count(p, "/") per match
	if idx := strings.Index(lp[e.nameStart:], q); idx >= 0 {
		score += 40 // whole query appears verbatim in the basename
		if idx == 0 {
			score += 20
		}
	}
	return score, pos, true
}

// topK is a hand-rolled min-heap of the best matches seen so far, where
// "less" means "worse": the heap root is always the result we'd drop first.
// Bounded at limit, so ranking is O(n log limit) instead of sorting every
// match, and we only allocate Pos slices for results that actually survive.
// Hand-rolled (not container/heap) so every comparison inlines and no
// interface boxing allocates per candidate.
type topK struct {
	items []FuzzyResult
	limit int
}

// worse reports whether a is a worse result than b: lower score, or equal
// score with a larger path (inverse of the final rank order).
func worse(a, b FuzzyResult) bool {
	if a.score != b.score {
		return a.score < b.score
	}
	return a.Path > b.Path
}

func (h *topK) len() int { return len(h.items) }

// keep reports whether (score, path) beats the worst result currently
// retained. Call only when the heap is full.
func (h *topK) keep(score int, path string) bool {
	w := h.items[0]
	return score > w.score || (score == w.score && path < w.Path)
}

func (h *topK) push(r FuzzyResult) {
	h.items = append(h.items, r)
	i := len(h.items) - 1
	for i > 0 {
		p := (i - 1) / 2
		if !worse(h.items[i], h.items[p]) {
			break
		}
		h.items[i], h.items[p] = h.items[p], h.items[i]
		i = p
	}
}

func (h *topK) pop() FuzzyResult {
	n := len(h.items) - 1
	h.items[0], h.items[n] = h.items[n], h.items[0]
	r := h.items[n]
	h.items = h.items[:n]
	i := 0
	for {
		l, rr := 2*i+1, 2*i+2
		m := i
		if l < n && worse(h.items[l], h.items[m]) {
			m = l
		}
		if rr < n && worse(h.items[rr], h.items[m]) {
			m = rr
		}
		if m == i {
			break
		}
		h.items[i], h.items[m] = h.items[m], h.items[i]
		i = m
	}
	return r
}

// pathMask returns a 64-bit bitmask of the characters in a lowercased path:
// a-z → bits 0-25, 0-9 → bits 26-35, common separators → bits 36-41, and
// everything else (including non-ASCII bytes) → bit 63. Prefilter: if the
// query's mask has any bit the path's mask lacks, the query cannot match,
// so the file is skipped without running the subsequence scan.
func pathMask(lower string) uint64 {
	var m uint64
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		var b uint64
		switch {
		case c >= 'a' && c <= 'z':
			b = uint64(c - 'a')
		case c >= '0' && c <= '9':
			b = 26 + uint64(c-'0')
		case c == '/':
			b = 36
		case c == '_':
			b = 37
		case c == '-':
			b = 38
		case c == '.':
			b = 39
		case c == ' ':
			b = 40
		case c == '@':
			b = 41
		default:
			b = 63
		}
		m |= 1 << b
	}
	return m
}

// scoreChunk scores files[lo:hi] and returns the best limit matches as a heap.
func scoreChunk(files []FileEntry, q, origQ string, qmask uint64, lo, hi, limit int) *topK {
	h := &topK{limit: limit}
	scratch := make([]int, 0, 64)
	for i := lo; i < hi; i++ {
		e := &files[i]
		// mask == 0 means "not computed" (entry built without pathMask):
		// scan it the slow way rather than silently excluding it.
		if e.mask != 0 && qmask&^e.mask != 0 {
			continue // query needs a character this path doesn't contain
		}
		s, pos, ok := fuzzyScore(q, origQ, e, scratch)
		if !ok {
			continue
		}
		if h.len() == limit && !h.keep(s, files[i].Path) {
			continue // can't make the cut — skip the Pos copy
		}
		if h.len() == limit {
			h.pop()
		}
		cp := make([]int, len(pos))
		copy(cp, pos)
		h.push(FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, score: s})
	}
	return h
}

// parallelThreshold bounds the per-query goroutine fan-out: below this many
// files the spawn/sync overhead exceeds the parallel gain, so we score inline.
const parallelThreshold = 4096

// FuzzyFind ranks every indexed path against query and returns the best limit.
func FuzzyFind(files []FileEntry, query string, limit int) []FuzzyResult {
	origQ := strings.ReplaceAll(strings.TrimSpace(query), " ", "")
	q := strings.ToLower(origQ)
	qmask := pathMask(q)

	if limit <= 0 {
		return nil
	}
	if q == "" {
		out := make([]FuzzyResult, 0, limit)
		for i := range files {
			if i == limit {
				break
			}
			out = append(out, FuzzyResult{Path: files[i].Path, Name: files[i].Name})
		}
		return out
	}

	var heaps []*topK
	if len(files) < parallelThreshold {
		heaps = []*topK{scoreChunk(files, q, origQ, qmask, 0, len(files), limit)}
	} else {
		workers := runtime.NumCPU()
		chunk := (len(files) + workers - 1) / workers
		nchunks := 0
		for lo := 0; lo < len(files); lo += chunk {
			nchunks++
		}
		heaps = make([]*topK, nchunks)
		var wg sync.WaitGroup
		for w := 0; w < nchunks; w++ {
			lo := w * chunk
			hi := min(lo+chunk, len(files))
			wg.Add(1)
			go func(w, lo, hi int) {
				defer wg.Done()
				heaps[w] = scoreChunk(files, q, origQ, qmask, lo, hi, limit)
			}(w, lo, hi)
		}
		wg.Wait()
	}

	// Merge the per-chunk heaps (at most nchunks*limit candidates) and take
	// the top limit in final rank order.
	total := 0
	for _, h := range heaps {
		total += h.len()
	}
	all := make([]FuzzyResult, 0, total)
	for _, h := range heaps {
		all = append(all, h.items...)
	}
	slices.SortFunc(all, func(a, b FuzzyResult) int {
		if a.score != b.score {
			return cmp.Compare(b.score, a.score) // higher score first
		}
		return strings.Compare(a.Path, b.Path)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

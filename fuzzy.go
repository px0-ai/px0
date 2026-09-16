package main

import (
	"container/heap"
	"runtime"
	"sort"
	"strings"
	"sync"
)

type FuzzyResult struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Pos   []int  `json:"pos"` // byte offsets in Path that matched, for highlighting
	score int
}

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
func fuzzyScore(q string, e *FileEntry, pos []int) (int, []int, bool) {
	p, lp := e.Path, e.lower
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
		if p[i] == q[k] {
			score += 4 // exact case
		}
		prev = i
	}
	// Prefer the shallower, shorter of two otherwise-equal paths.
	score -= len(p) / 8
	score -= strings.Count(p, "/") * 2
	if idx := strings.Index(e.lower[e.nameStart:], q); idx >= 0 {
		score += 40 // whole query appears verbatim in the basename
		if idx == 0 {
			score += 20
		}
	}
	return score, pos, true
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// fuzzyHeap keeps the best K matches seen so far with the worst of them at
// the root, so each candidate costs one comparison against it (and a Pos copy
// only when it survives). Its order mirrors the final rank: higher score
// wins, ties break toward the smaller path.
type fuzzyHeap []FuzzyResult

func (h fuzzyHeap) Len() int { return len(h) }
func (h fuzzyHeap) Less(i, j int) bool {
	if h[i].score != h[j].score {
		return h[i].score < h[j].score
	}
	return h[i].Path > h[j].Path
}
func (h fuzzyHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *fuzzyHeap) Push(x any)   { *h = append(*h, x.(FuzzyResult)) }
func (h *fuzzyHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// beats reports whether score/path outranks the heap's current worst (root).
// The heap must be non-empty: callers only ask once it holds K entries.
func (h fuzzyHeap) beats(score int, path string) bool {
	w := h[0]
	if score != w.score {
		return score > w.score
	}
	return path < w.Path
}

// FuzzyFind ranks every indexed path against query and returns the best limit.
// Scoring still touches every file, but only K results are ever retained and
// sorted: O(N log K) instead of O(N log N), and non-survivors skip the Pos
// copy entirely. The returned order matches the full sort exactly.
func FuzzyFind(files []FileEntry, query string, limit int) []FuzzyResult {
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.ReplaceAll(q, " ", "")

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
	if limit <= 0 {
		return nil
	}

	workers := runtime.NumCPU()
	chunk := (len(files) + workers - 1) / workers
	if chunk == 0 {
		chunk = 1
	}
	parts := make([]fuzzyHeap, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w * chunk
		if lo >= len(files) {
			break
		}
		hi := min(lo+chunk, len(files))
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			local := &fuzzyHeap{}
			scratch := make([]int, 0, 64)
			for i := lo; i < hi; i++ {
				s, pos, ok := fuzzyScore(q, &files[i], scratch)
				if !ok {
					continue
				}
				if local.Len() >= limit {
					if !local.beats(s, files[i].Path) {
						continue
					}
					cp := make([]int, len(pos))
					copy(cp, pos)
					(*local)[0] = FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, score: s}
					heap.Fix(local, 0)
					continue
				}
				cp := make([]int, len(pos))
				copy(cp, pos)
				heap.Push(local, FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, score: s})
			}
			parts[w] = *local
		}(w, lo, hi)
	}
	wg.Wait()

	// At most workers*limit candidates (8*500 worst case): one small sort.
	var all []FuzzyResult
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].Path < all[j].Path
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

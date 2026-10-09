package main

import (
	"px0/internal/fuzzy"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// FuzzyResult represents a matched file path ranked by the fuzzy search engine.
// Re-exported from internal/fuzzy.Result.
type FuzzyResult = fuzzy.Result

// isBoundary reports whether a byte acts as a word or segment boundary.
func isBoundary(b byte) bool {
	return fuzzy.IsBoundary(b)
}

// fuzzyScore delegates to internal/fuzzy.Score.
func fuzzyScore(q, origQ string, e *FileEntry, pos []int) (int, []int, bool) {
	return fuzzy.Score(q, origQ, e.Path, e.lower, e.nameStart, pos)
}

// FuzzyFind ranks every indexed path against query and returns the best limit.
func FuzzyFind(files []FileEntry, query string, limit int) []FuzzyResult {
	origQ := strings.ReplaceAll(strings.TrimSpace(query), " ", "")
	q := strings.ToLower(origQ)

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

	workers := runtime.NumCPU()
	chunk := (len(files) + workers - 1) / workers
	if chunk == 0 {
		chunk = 1
	}
	parts := make([][]FuzzyResult, workers)
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
			local := make([]FuzzyResult, 0, 64)
			scratch := make([]int, 0, 64)
			for i := lo; i < hi; i++ {
				s, pos, ok := fuzzy.Score(q, origQ, files[i].Path, files[i].lower, files[i].nameStart, scratch)
				if !ok {
					continue
				}
				cp := make([]int, len(pos))
				copy(cp, pos)
				local = append(local, FuzzyResult{Path: files[i].Path, Name: files[i].Name, Pos: cp, Score: s})
			}
			parts[w] = local
		}(w, lo, hi)
	}
	wg.Wait()

	var all []FuzzyResult
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Path < all[j].Path
	})
	if len(all) > limit {
		all = all[:limit]
	}
	return all
}

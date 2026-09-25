package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Call trails: who calls a function, and what it calls, one level at a time.
// The server identifies each function by an opaque CallHierarchyItem that has
// to be handed back verbatim to expand the next level. px0 keeps no state
// between requests, so every node carries its item to the browser, which sends
// it back when the reader expands that node.

// CallNode is one function in a call trail.
type CallNode struct {
	Name     string `json:"name"`
	Detail   string `json:"detail,omitempty"`
	Kind     string `json:"kind"`
	Path     string `json:"path"` // where the function is declared
	Line     int    `json:"line"`
	Ext      bool   `json:"ext,omitempty"`
	SitePath string `json:"sitePath,omitempty"` // file holding the call expressions
	Sites    []int  `json:"sites,omitempty"`    // lines of those calls, ascending
	Item     string `json:"item"`               // CallHierarchyItem, returned as-is to expand
}

// lspCallItem represents the LSP wire format for CallHierarchyItem
// defined in the Language Server Protocol specification.
type lspCallItem struct {
	Name           string   `json:"name"`
	Kind           int      `json:"kind"`
	Detail         string   `json:"detail"`
	URI            string   `json:"uri"`
	SelectionRange lspRange `json:"selectionRange"`
}

// treePath maps an absolute path to what the UI shows and opens: relative
// inside the indexed tree, absolute outside it. Only paths a language server
// named may be allowlisted for opening; paths that arrived from the browser
// must pass allow=false.
func (m *lspManager) treePath(abs string, allow bool) (rel string, ext bool) {
	rel, err := filepath.Rel(m.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		// The standard library, or a dependency in the module cache.
		if allow {
			m.allow(abs)
		}
		return filepath.ToSlash(abs), true
	}
	return filepath.ToSlash(rel), false
}

func (m *lspManager) callNode(raw json.RawMessage, allow bool) (CallNode, lspCallItem, error) {
	var it lspCallItem
	if err := json.Unmarshal(raw, &it); err != nil || it.URI == "" {
		return CallNode{}, it, fmt.Errorf("bad call hierarchy item")
	}
	abs, err := uriToPath(it.URI)
	if err != nil {
		return CallNode{}, it, err
	}
	rel, ext := m.treePath(abs, allow)
	kind := lspSymbolKind[it.Kind]
	if kind == "" {
		kind = "sym"
	}
	return CallNode{
		Name: it.Name, Detail: it.Detail, Kind: kind,
		Path: rel, Line: it.SelectionRange.Start.Line + 1, Ext: ext,
		Item: string(raw),
	}, it, nil
}

// PrepareCalls resolves the function at a position into the root(s) of a trail.
func (m *lspManager) PrepareCalls(ctx context.Context, abs, rel string, line, u16col int) ([]CallNode, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	if err := c.ensureOpen(abs, rel); err != nil {
		return nil, err
	}
	d, err := Open(abs, rel)
	if err != nil {
		return nil, err
	}
	byteCol := utf16ToByte(d.Raw(line), u16col)

	var raw []json.RawMessage
	if err := c.call(ctx, "textDocument/prepareCallHierarchy", map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(abs)},
		"position":     c.toLSP(d.Raw(line), line, byteCol),
	}, &raw); err != nil {
		return nil, err
	}
	out := make([]CallNode, 0, len(raw))
	for _, r := range raw {
		if n, _, err := m.callNode(r, true); err == nil {
			out = append(out, n)
		}
	}
	return out, nil
}

// Calls expands one node of a trail: its callers, or with outgoing its callees.
// rel picks the language server; item is a node's Item as the browser sent it.
func (m *lspManager) Calls(ctx context.Context, rel, item string, outgoing bool) ([]CallNode, error) {
	c, err := m.client(ctx, rel)
	if err != nil {
		return nil, err
	}
	_, parent, err := m.callNode(json.RawMessage(item), false)
	if err != nil {
		return nil, err
	}
	// Some servers only answer about documents they were handed. Only files in
	// the tree are read for this: the item came from the browser.
	if pabs, err := uriToPath(parent.URI); err == nil {
		if prel, ext := m.treePath(pabs, false); !ext {
			c.ensureOpen(pabs, prel)
		}
	}

	method := "callHierarchy/incomingCalls"
	if outgoing {
		method = "callHierarchy/outgoingCalls"
	}
	var res []struct {
		From       json.RawMessage `json:"from"`
		To         json.RawMessage `json:"to"`
		FromRanges []lspRange      `json:"fromRanges"`
	}
	if err := c.call(ctx, method, map[string]any{"item": json.RawMessage(item)}, &res); err != nil {
		return nil, err
	}

	out := make([]CallNode, 0, len(res))
	for _, r := range res {
		raw := r.From
		if outgoing {
			raw = r.To
		}
		n, it, err := m.callNode(raw, true)
		if err != nil {
			continue
		}
		// fromRanges sit inside the caller: the returned function for incoming
		// calls, the function being expanded for outgoing ones.
		siteURI := it.URI
		if outgoing {
			siteURI = parent.URI
		}
		if sabs, err := uriToPath(siteURI); err == nil {
			n.SitePath, _ = m.treePath(sabs, false)
		}
		n.Sites = siteLines(r.FromRanges)
		out = append(out, n)
	}

	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if outgoing {
			return firstSite(a) < firstSite(b) // callees in the order they are called
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	return out, nil
}

func siteLines(rs []lspRange) []int {
	seen := map[int]bool{}
	var out []int
	for _, r := range rs {
		l := r.Start.Line + 1
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	sort.Ints(out)
	return out
}

func firstSite(n CallNode) int {
	if len(n.Sites) == 0 {
		return 0
	}
	return n.Sites[0]
}

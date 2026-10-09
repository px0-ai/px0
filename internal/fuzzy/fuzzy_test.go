package fuzzy

import (
	"strings"
	"testing"
)

func subsequence(sub, s string) bool {
	si := 0
	for i := 0; i < len(s) && si < len(sub); i++ {
		if s[i] == sub[si] {
			si++
		}
	}
	return si == len(sub)
}

func TestFuzzyRanking(t *testing.T) {
	t.Parallel()

	paths := []string{
		"internal/server/http_server.go",
		"cmd/px0/main.go",
		"web/app.js",
		"pkg/util/strings.go",
		"vendor/github.com/x/http/server.go",
		"httpserver.go",
	}
	items := make([]Item, len(paths))
	for i, p := range paths {
		items[i] = NewItem(p)
	}

	got := Find(items, "httpserver", 10)
	if len(got) == 0 {
		t.Fatal("no matches for httpserver")
	}
	if got[0].Path != "httpserver.go" {
		t.Errorf("best match for httpserver = %q, want httpserver.go", got[0].Path)
	}

	for _, r := range got {
		if !subsequence("httpserver", strings.ToLower(r.Path)) {
			t.Errorf("%q is not a subsequence match", r.Path)
		}
		for _, p := range r.Pos {
			if p < 0 || p >= len(r.Path) {
				t.Errorf("%q: highlight position %d out of range", r.Path, p)
			}
		}
	}

	if got := Find(items, "zzqq", 10); len(got) != 0 {
		t.Errorf("expected no matches, got %v", got)
	}

	if got := Find(items, "appjs", 10); len(got) == 0 || got[0].Path != "web/app.js" {
		t.Errorf("expected web/app.js for appjs, got %v", got)
	}
}

func TestFuzzyCaseSensitivity(t *testing.T) {
	t.Parallel()

	items := []Item{
		NewItem("src/HTTPServer.go"),
		NewItem("src/httpserver.go"),
	}

	res := Find(items, "HTTPServer", 10)
	if len(res) < 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].Path != "src/HTTPServer.go" {
		t.Errorf("expected 'src/HTTPServer.go' to rank higher for query 'HTTPServer', got: %s", res[0].Path)
	}
}

func TestFuzzyEmptyQuery(t *testing.T) {
	t.Parallel()

	items := []Item{
		NewItem("a.go"),
		NewItem("b.go"),
		NewItem("c.go"),
	}
	got := Find(items, "", 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 items for empty query limit 2, got %d", len(got))
	}
	if got[0].Path != "a.go" || got[1].Path != "b.go" {
		t.Errorf("unexpected results: %+v", got)
	}
}

func TestIsBoundary(t *testing.T) {
	t.Parallel()

	boundaries := []byte{'/', '_', '-', '.', ' ', '@'}
	for _, b := range boundaries {
		if !IsBoundary(b) {
			t.Errorf("expected byte %q to be boundary", b)
		}
	}
	nonBoundaries := []byte{'a', 'Z', '0', '!', ':'}
	for _, nb := range nonBoundaries {
		if IsBoundary(nb) {
			t.Errorf("expected byte %q not to be boundary", nb)
		}
	}
}

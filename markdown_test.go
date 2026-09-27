package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownPreview(t *testing.T) {
	s, root := newTestServer(t)
	src := "# Hello World\n\nSee [deep](sub/deep.py#L2).\n\n## API_reference\n\n```go\nfunc main() {}\n```\n\n- [x] done\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	code, m := get(t, s, "/api/markdown?path=README.md")
	if code != 200 {
		t.Fatalf("status %d: %v", code, m)
	}
	out, _ := m["html"].(string)
	for _, want := range []string{
		`<h1 id="hello-world" data-line="1">Hello World</h1>`,
		`<h2 id="api_reference" data-line="5">`,
		`<pre class="md-code" data-line="8" data-lang="go"><code><i class=k>func</i>`,
		`<a href="sub/deep.py#L2">deep</a>`,
		`type="checkbox"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview is missing %q:\n%s", want, out)
		}
	}

	if _, m := get(t, s, "/api/file?path=README.md"); m["markdown"] != true {
		t.Errorf("file response should flag Markdown, got %v", m["markdown"])
	}
	if code, _ := get(t, s, "/api/markdown?path=main.go"); code != 415 {
		t.Errorf("non-Markdown file: status %d, want 415", code)
	}
	if code, _ := get(t, s, "/api/markdown?path=../x.md"); code != 400 {
		t.Errorf("path outside the root: status %d, want 400", code)
	}
}

func TestMermaidFenceKeepsRawSource(t *testing.T) {
	// A mermaid fence must reach the client as raw, un-highlighted source so the
	// browser can hand it to mermaid.js. It carries data-lang="mermaid" and must
	// never be run through the syntax highlighter (no token <i class=...> spans),
	// regardless of whether Chroma later ships a mermaid lexer.
	out, err := renderMarkdown([]byte("```mermaid\ngraph TD\n  A[Start] --> B{OK?}\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `data-lang="mermaid"`) {
		t.Errorf("mermaid fence lost its language tag:\n%s", out)
	}
	if !strings.Contains(out, "A[Start] --&gt; B{OK?}") {
		t.Errorf("mermaid source not preserved verbatim (escaped):\n%s", out)
	}
	if strings.Contains(out, "<i class=") {
		t.Errorf("mermaid fence should not be syntax-highlighted:\n%s", out)
	}
}

func TestHeadingIDsFollowGitHub(t *testing.T) {
	ids := &headingIDs{seen: map[string]bool{}}
	for _, c := range []struct{ in, want string }{
		{"Getting Started!", "getting-started"},
		{"snake_case API", "snake_case-api"},
		{"Überblick", "überblick"},
		{"Getting Started", "getting-started-1"},
		{"???", "section"},
	} {
		if got := string(ids.Generate([]byte(c.in), 0)); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}

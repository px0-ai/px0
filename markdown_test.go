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

func TestFrontmatter(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		want string
	}{
		{
			name: "yaml_basic",
			src:  []byte("---\ntitle: test\ntags: [a, b]\n---\n# Hello\n"),
			want: `<h1 id="hello" data-line="5">Hello</h1>`,
		},
		{
			name: "toml_basic",
			src:  []byte("+++\ntitle = \"test\"\n+++\n# Hello\n"),
			want: `<h1 id="hello" data-line="4">Hello</h1>`,
		},
		{
			name: "eof",
			src:  []byte("---\ntitle: test\n---"),
			want: ``,
		},
		{
			name: "delimiter_like_content",
			src:  []byte("---\ntitle: test\nfoo: ---\nbar: +++\n---\n# Hello\n"),
			want: `<h1 id="hello" data-line="6">Hello</h1>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := renderMarkdown(tc.src)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "title:") || strings.Contains(out, "title =") {
				t.Errorf("frontmatter was not stripped! OUT:\n%s", out)
			}
			if tc.want != "" && !strings.Contains(out, tc.want) {
				t.Errorf("expected to find %q in OUT:\n%s", tc.want, out)
			}
		})
	}
}

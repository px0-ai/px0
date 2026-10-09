package ignore

import (
	"testing"
)

func TestIgnoreFastPathMatchesRegex(t *testing.T) {
	t.Parallel()
	patterns := []string{
		"node_modules",
		"*.pyc",
		"build/",
		"vendor/",
		"a/b/c",
		"*.log",
		"test*file",
		"sub/*.txt",
	}
	paths := []string{
		"node_modules",
		"sub/node_modules",
		"sub/node_modules/x.js",
		"a.pyc",
		"pkg/a.pyc",
		"build",
		"build/out.js",
		"vendor",
		"vendor/pkg/a.go",
		"a/b/c",
		"a/b/c/d",
		"a.log",
		"test123file",
		"sub/test.txt",
		"other/sub/test.txt",
		"unrelated.go",
	}

	for _, pat := range patterns {
		for _, dirOnlySuffix := range []string{"", "/"} {
			full := pat + dirOnlySuffix
			r, ok := CompilePattern(full)
			if !ok {
				continue
			}
			slow, ok := CompilePatternRegex(full)
			if !ok {
				t.Fatalf("%q: regex form failed to compile", full)
			}
			for _, p := range paths {
				for _, isDir := range []bool{false, true} {
					got := r.Hit(p, isDir)
					want := slow.Hit(p, isDir)
					if got != want {
						t.Errorf("pattern %q path %q dir=%v: fast=%v regex=%v",
							full, p, isDir, got, want)
					}
				}
			}
		}
	}
}

func TestIgnoreSetBasics(t *testing.T) {
	t.Parallel()
	set := New([]string{"custom_ignored/", "*.tmp"})
	if !set.Match("node_modules/foo.js", false) {
		t.Error("expected node_modules to be ignored")
	}
	if !set.Match("custom_ignored", true) {
		t.Error("expected custom_ignored dir to be ignored")
	}
	if !set.Match("a/b/c.tmp", false) {
		t.Error("expected *.tmp to be ignored")
	}
	if set.Match("main.go", false) {
		t.Error("main.go should not be ignored")
	}
}

package render

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestScanFolds(t *testing.T) {
	for _, tc := range []struct {
		name, path, src string
		want            []FoldRange
	}{
		{"go nested", "a.go", "func f() {\n if ok {\n  work()\n }\n}\n", []FoldRange{{1, 5}, {2, 4}}},
		{"go imports", "a.go", "package p\n\nimport (\n \"os\"\n\n alias \"path/filepath\"\n)\n", []FoldRange{{3, 7}}},
		{"go imports with intervening comment", "a.go", "import /* (\n ) */\n(\n \"os\"\n)\n", []FoldRange{{1, 5}}},
		{"go grouped declarations", "a.go", "const (\n A = 1\n)\nvar (\n value = call(\n  1,\n )\n)\ntype (\n Handler func(\n  int,\n )\n)\n", []FoldRange{{1, 3}, {4, 8}, {9, 13}}},
		{"go ordinary parentheses", "a.go", "import \"os\"\nvar value = call(\n 1,\n)\nfunc f(\n x int,\n) {\n call(\n  x,\n )\n}\n", []FoldRange{{7, 11}}},
		{"go declaration words inside literals", "a.go", "var text = `import (\n \"os\"\n)`\n// const (\n/* var ( */\n", nil},
		{"go single-line group", "a.go", "import (\"os\")\n", nil},
		{"go strings and comments", "a.go", "func f() {\n s := `}\n{`\n // }\n /* {\n } */\n println(\"{\\\"}\", '{')\n}\n", []FoldRange{{1, 8}}},
		{"same line", "a.go", "func f() { return }\n", nil},
		{"shared closing line", "a.js", "if (ok) {\n work()\n} else {\n other()\n}\n", []FoldRange{{1, 2}, {3, 5}}},
		{"regex", "a.ts", "function f() {\n const r = /[{}\\/]/g;\n if (x) /}/.test(x);\n return /{/;\n}\n", []FoldRange{{1, 5}}},
		{"division", "a.js", "function f() {\n const x = foo() / 2;\n return x / 3;\n}\n", []FoldRange{{1, 4}}},
		{"nested templates", "a.ts", "function f() {\n const x = `text } ${foo({\n  bar: `nested ${x}`\n })}`;\n}\n", []FoldRange{{1, 5}, {2, 3}}},
		{"cpp raw", "a.cpp", "void f() {\n auto x = R\"tag(a \" }\n {)tag\";\n}\n", []FoldRange{{1, 4}}},
		{"java text block", "a.java", "class A {\n String x = \"\"\"\n } { \"\n \"\"\";\n}\n", []FoldRange{{1, 5}}},
		{"json", "a.json", "{\n \"items\": [\n  {\"x\": \"}\"}\n ]\n}\n", []FoldRange{{1, 5}, {2, 4}}},
		{"python suites", "a.py", "def f():\n    if ok:\n        work()\n    else:\n        other()\n\n# comment\nnext()\n", []FoldRange{{1, 5}, {2, 3}, {4, 5}}},
		{"python multiline signature", "a.py", "def f(\n    x: str,\n):\n    return x\nnext()\n", []FoldRange{{1, 4}}},
		{"python strings", "a.py", "def f():\n    text = \"\"\"\nnot a suite:\n    still text\n\"\"\"\n    return text\nnext()\n", []FoldRange{{1, 6}}},
		{"python comments blank tabs", "a.py", "def f():\n\tif x:\n\t\twork()\n\n\t# comment:\n\treturn x # :\n", []FoldRange{{1, 6}, {2, 3}}},
		{"unmatched", "a.go", "func f() {\n work()\n", nil},
		{"unsupported", "a.txt", "{\n x\n}\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, limited, err := scanFolds(context.Background(), tc.src, foldingKind(tc.path))
			if err != nil || limited || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v limited=%v err=%v, want %v", got, limited, err, tc.want)
			}
		})
	}
}

func TestFoldingLimitsAndCancellation(t *testing.T) {
	for _, src := range []string{strings.Repeat("x", maxFoldBytes+1), strings.Repeat("{", maxFoldDepth+1)} {
		got, limited, err := scanFolds(context.Background(), src, foldGo)
		if !limited || len(got) != 0 || err != nil {
			t.Fatalf("limits: %v %v %v", got, limited, err)
		}
	}
	got, limited, err := scanFolds(context.Background(), strings.Repeat("{\nx\n}\n", maxFoldRanges+10), foldGo)
	if err != nil || !limited || len(got) != maxFoldRanges {
		t.Fatalf("range cap: %d %v %v", len(got), limited, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := NewDoc("func f() {\n work()\n}\n", "a.go")
	if _, _, err := d.Folds(ctx); err == nil {
		t.Fatal("expected cancellation")
	}
	got, _, err = d.Folds(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("cancelled scan must be retryable: %v %v", got, err)
	}
}

func TestFoldsConcurrentAndLazy(t *testing.T) {
	d := NewDoc("func f() {\n work()\n}\n", "a.go")
	if d.folds != nil {
		t.Fatal("folding allocated during open")
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, _, err := d.Folds(context.Background())
			if err != nil || len(got) != 1 {
				t.Errorf("folds: %v %v", got, err)
			}
		}()
	}
	wg.Wait()
	first, _, _ := d.Folds(context.Background())
	second, _, _ := d.Folds(context.Background())
	if &first[0] != &second[0] {
		t.Fatal("cached range storage was copied")
	}
	if len(d.chunks) != 0 {
		t.Fatal("folding must not populate highlighted chunks")
	}
}

func TestFoldingChargesDocumentCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.go")
	if err := os.WriteFile(path, []byte("func f() {\n work()\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	d, err := Open(path, "a.go")
	if err != nil {
		t.Fatal(err)
	}
	defer Evict(path)
	cache.mu.Lock()
	before := d.bytes
	cache.mu.Unlock()
	ranges, _, err := d.Folds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	after := d.bytes
	cache.mu.Unlock()
	if after-before < cap(ranges)*8+192 {
		t.Fatal("fold buffers were not charged to the existing document cache")
	}
	d.Folds(context.Background())
	cache.mu.Lock()
	again := d.bytes
	cache.mu.Unlock()
	if again != after {
		t.Fatal("cached lookup charged metadata twice")
	}
}

func TestRawTextRange(t *testing.T) {
	d := NewDoc("one\ntwo\nthree\n", "a.go")
	for _, tc := range []struct {
		start, end int
		want       string
	}{{0, 1, "one"}, {1, 3, "two\nthree"}, {-1, 99, "one\ntwo\nthree\n"}, {2, 2, ""}, {8, 9, ""}} {
		if got := d.RawText(tc.start, tc.end); got != tc.want {
			t.Errorf("RawText(%d,%d) = %q, want %q", tc.start, tc.end, got, tc.want)
		}
	}
}

func BenchmarkFoldScan(b *testing.B) {
	for _, kind := range []struct {
		name, source string
		kind         uint8
	}{
		{"Go", "func f() {\n if ok {\n  println(\"}\")\n }\n}\n", foldGo},
		{"Python", "def f():\n    if ok:\n        return 'x'\n    return ''\n", foldPython},
	} {
		b.Run(kind.name, func(b *testing.B) {
			src := strings.Repeat(kind.source, 2000)
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = scanFolds(context.Background(), src, kind.kind)
			}
		})
	}
}

func BenchmarkFoldsCached(b *testing.B) {
	d := NewDoc(strings.Repeat("func f() {\n work()\n}\n", 2000), "a.go")
	d.Folds(context.Background())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Folds(context.Background())
	}
}

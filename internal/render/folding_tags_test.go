package render

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestTagFolds(t *testing.T) {
	for _, tc := range []struct {
		name, path, src string
		want            []FoldRange
	}{
		{"nested HTML", "a.html", "<!doctype html>\n<HTML>\n<body>\n<section>\ntext\n</section>\n</body>\n</html>\n", []FoldRange{{2, 8}, {3, 7}, {4, 6}}},
		{"quoted multiline attributes", "a.htm", "<section\n title=\"> <fake>\"\n data-x='</section>'>\ntext\n</section>\n", []FoldRange{{1, 5}}},
		{"self closing", "a.xml", "<root>\n<item\n value=\">\" />\n<child/>\n</root>\n", []FoldRange{{1, 5}}},
		{"shared closing line", "a.html", "<main>\n<section>\ntext\n</section></main>\n", []FoldRange{{1, 4}, {2, 3}}},
		{"trailing text", "a.html", "<div>\ntext\n</div> visible\n<section>\nx\n</section>\n", []FoldRange{{1, 2}, {4, 6}}},
		{"comments", "a.html", "<main>\n<!--\n<fake>\n</fake>\n-->\n</main>\n", []FoldRange{{1, 6}, {2, 5}}},
		{"XML names", "a.xml", "<?xml version='1.0'?>\n<ns:Root>\n<ns:Child>\nx\n</ns:Child>\n</ns:Root>\n", []FoldRange{{2, 6}, {3, 5}}},
		{"XML case sensitive", "a.xml", "<Root>\n<Child>\nx\n</child>\n</Root>\n", []FoldRange{{1, 5}}},
		{"XML input is paired", "a.xhtml", "<input>\nx\n</input>\n", []FoldRange{{1, 3}}},
		{"XML script is markup", "a.xml", "<script>\n<child>\nx\n</child>\n</script>\n", []FoldRange{{1, 5}, {2, 4}}},
		{"CDATA", "a.svg", "<svg>\n<![CDATA[\n<fake>\n</fake>\n]]>\n</svg>\n", []FoldRange{{1, 6}, {2, 5}}},
		{"DTD and processing instruction", "a.xml", "<!DOCTYPE root [\n<!ENTITY x '<fake>'>\n<!-- ] > -->\n]>\n<?process '<fake>'?>\n<root>\nx\n</root>\n", []FoldRange{{6, 8}}},
		{"SVG foreign elements", "a.html", "<svg>\n<linearGradient>\nx\n</lineargradient>\n<foreignObject>\n<DIV>\ntext\n</div>\n</foreignObject>\n</svg>\n", []FoldRange{{1, 10}, {5, 9}, {6, 8}}},
		{"unmatched descendant", "a.html", "<main>\n<section>\nx\n</main>\n", []FoldRange{{1, 4}}},
		{"incomplete tag recovery", "a.html", "<broken\n<section>\nx\n</section>\n", []FoldRange{{2, 4}}},
		{"unclosed element", "a.html", "<main>\nx\n", nil},
		{"unclosed attribute", "a.html", "<main title='\n<section>\nx\n</section>\n", nil},
		{"unclosed comment", "a.xml", "<!--\n<root>\nx\n</root>\n", nil},
		{"single line", "a.html", "<div>x</div>\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, limited, err := scanFolds(context.Background(), tc.src, foldingKind(tc.path))
			if err != nil || limited || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v limited=%v err=%v, want %v", got, limited, err, tc.want)
			}
		})
	}
}

func TestHTMLVoidAndRawText(t *testing.T) {
	voids := "area base br col embed hr img input link meta param source track wbr"
	src := "<main>\n" + strings.Repeat("<"+strings.ReplaceAll(strings.ToUpper(voids), " ", "><")+">\n", 30) + "</main>\n"
	got, limited, err := scanFolds(context.Background(), src, foldHTML)
	if err != nil || limited || !reflect.DeepEqual(got, []FoldRange{{1, 32}}) {
		t.Fatalf("void elements consumed stack depth: %v %v %v", got, limited, err)
	}
	for _, name := range strings.Fields("script style textarea title xmp iframe noembed noframes") {
		t.Run(name, func(t *testing.T) {
			src := "<" + name + ">\n<fake>\n</" + name + "ure>\n</fake>\n</" + strings.ToUpper(name) + ">\n"
			got, limited, err := scanFolds(context.Background(), src, foldHTML)
			if err != nil || limited || !reflect.DeepEqual(got, []FoldRange{{1, 5}}) {
				t.Fatalf("raw text: %v %v %v", got, limited, err)
			}
		})
	}
	got, limited, err = scanFolds(context.Background(), "<plaintext>\n<fake>\nx\n</fake>\n</plaintext>\n", foldHTML)
	if err != nil || limited || len(got) != 0 {
		t.Fatalf("plaintext: %v %v %v", got, limited, err)
	}
}

func TestTagFoldingKindsAndLimits(t *testing.T) {
	for _, ext := range strings.Fields("xml xhtml xht svg mathml mml xsl xslt xaml axml fxml plist resx xsd wsdl rss atom xlf xliff") {
		if foldingKind("file."+strings.ToUpper(ext)) != foldXML {
			t.Errorf("missing XML extension %s", ext)
		}
	}
	for _, ext := range []string{"html", "htm"} {
		if foldingKind("file."+strings.ToUpper(ext)) != foldHTML {
			t.Errorf("missing HTML extension %s", ext)
		}
	}
	for _, ext := range []string{"vue", "svelte", "astro", "mdx"} {
		if foldingKind("file."+ext) != 0 {
			t.Errorf("unexpected framework tag support %s", ext)
		}
	}
	for _, kind := range []uint8{foldHTML, foldXML} {
		for _, src := range []string{strings.Repeat("x", maxFoldBytes+1), strings.Repeat("<div>", maxFoldDepth+1)} {
			got, limited, err := scanFolds(context.Background(), src, kind)
			if err != nil || !limited || len(got) != 0 {
				t.Fatalf("limits: %d %v %v", len(got), limited, err)
			}
		}
		got, limited, err := scanFolds(context.Background(), strings.Repeat("<div>\nx\n</div>\n", maxFoldRanges+1), kind)
		if err != nil || !limited || len(got) != maxFoldRanges {
			t.Fatalf("range cap: %d %v %v", len(got), limited, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, _, err := scanFolds(ctx, "<div>\nx\n</div>", kind); err != context.Canceled {
			t.Fatalf("cancellation: %v", err)
		}
	}
}

// Deterministically cancel after scanning has started, without timing races.
type tagScanCancelContext struct {
	context.Context
	checks int
}

func (c *tagScanCancelContext) Err() error {
	c.checks++
	if c.checks >= 4 {
		return context.Canceled
	}
	return nil
}

func TestTagFoldingCancellationDuringScan(t *testing.T) {
	for _, src := range []string{
		strings.Repeat("<div>\nx\n</div>\n", 100),
		"<!DOCTYPE root [" + strings.Repeat(" ", 16384) + "]>",
		"<root " + strings.Repeat(" ", 16384) + ">",
		"<script>" + strings.Repeat("<fake>", 100) + "</script>",
	} {
		ctx := &tagScanCancelContext{Context: context.Background()}
		got, limited, err := scanFolds(ctx, src, foldHTML)
		if err != context.Canceled || limited || len(got) != 0 {
			t.Fatalf("partial results on cancellation: %v %v %v", got, limited, err)
		}
	}
}

func BenchmarkTagFoldScan(b *testing.B) {
	for _, tc := range []struct {
		name, source string
		kind         uint8
	}{
		{"HTML", "<section class='item'>\n<img src='x'>\n<script>\nconst text = '<fake>';\n</script>\n</section>\n", foldHTML},
		{"XML", "<ns:entry key='value'>\n<ns:text>\n<![CDATA[\n<literal>\n]]>\n</ns:text>\n</ns:entry>\n", foldXML},
	} {
		b.Run(tc.name, func(b *testing.B) {
			src := strings.Repeat(tc.source, 2000)
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = scanFolds(context.Background(), src, tc.kind)
			}
		})
	}
}

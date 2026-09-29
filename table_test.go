package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func mustTable(t *testing.T, src string, delim rune) tableResult {
	t.Helper()
	res, err := renderTable(strings.NewReader(src), int64(len(src)), delim)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestTableQuotedCellWithCommaAndNewlineStaysOneCellAndLinesSkip(t *testing.T) {
	res := mustTable(t, "repo,note\nchromium,\"monorepo,\ndepot_tools excluded\"\nvscode,warm\n", ',')
	if got := res.Rows[0].Cells[1]; got != "monorepo,\ndepot_tools excluded" {
		t.Errorf("quoted cell = %q", got)
	}
	if res.HeaderLine != 1 || res.Rows[0].Line != 2 || res.Rows[1].Line != 4 {
		t.Errorf("lines = header %d, rows %d %d; want 1, 2 4", res.HeaderLine, res.Rows[0].Line, res.Rows[1].Line)
	}
}

func TestTableRaggedRowsKeepTheirCellsAndWidenTheTable(t *testing.T) {
	res := mustTable(t, "a,b,c\n1\n1,2,3,4\n", ',')
	if len(res.Rows[0].Cells) != 1 || len(res.Rows[1].Cells) != 4 {
		t.Errorf("rows = %v", res.Rows)
	}
	if res.Cols != 4 {
		t.Errorf("cols = %d, want 4", res.Cols)
	}
}

func TestTableTSVSplitsOnTabsOnly(t *testing.T) {
	res := mustTable(t, "name\tcity\nDoe, Jane\tLeeds\n", '\t')
	if want := []string{"Doe, Jane", "Leeds"}; !reflect.DeepEqual(res.Rows[0].Cells, want) {
		t.Errorf("cells = %q, want %q", res.Rows[0].Cells, want)
	}
}

func TestTableStripsByteOrderMarkFromHeader(t *testing.T) {
	res := mustTable(t, "\ufeffid,name\n1,x\n", ',')
	if res.Header[0] != "id" {
		t.Errorf("header[0] = %q", res.Header[0])
	}
}

func TestTableBlankLinesAreSkippedButLineNumbersStayTrue(t *testing.T) {
	res := mustTable(t, "a,b\n\n1,2\n\n\n3,4\n", ',')
	if len(res.Rows) != 2 || res.Rows[0].Line != 3 || res.Rows[1].Line != 6 {
		t.Errorf("rows = %+v", res.Rows)
	}
}

func TestTableStrayQuotesDoNotFailTheFile(t *testing.T) {
	res := mustTable(t, "a,b\n5\" pipe,x\n", ',')
	if res.Rows[0].Cells[0] != "5\" pipe" {
		t.Errorf("cell = %q", res.Rows[0].Cells[0])
	}
}

func TestTableEmptyFileHasNoColumns(t *testing.T) {
	res := mustTable(t, "", ',')
	if res.Cols != 0 || len(res.Rows) != 0 || res.Truncated {
		t.Errorf("res = %+v", res)
	}
}

func TestTableStopsAtRowCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("n,sq\n")
	for i := 1; i <= maxTableRows+1000; i++ {
		fmt.Fprintf(&b, "%d,%d\n", i, i*i)
	}
	res := mustTable(t, b.String(), ',')
	if len(res.Rows) != maxTableRows || !res.Truncated {
		t.Errorf("rows %d truncated %v; want %d true", len(res.Rows), res.Truncated, maxTableRows)
	}
	if last := res.Rows[len(res.Rows)-1]; last.Line != maxTableRows+1 {
		t.Errorf("last row line %d", last.Line)
	}
}

func TestTableExactlyAtRowCapIsNotTruncated(t *testing.T) {
	var b strings.Builder
	b.WriteString("n\n")
	for i := 1; i <= maxTableRows; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	if res := mustTable(t, b.String(), ','); res.Truncated {
		t.Error("a file of exactly the cap should not say it was cut")
	}
}

func writeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTableEndpointServesRowsAndFileFlagsTables(t *testing.T) {
	s, root := newTestServer(t)
	writeFile(t, root, "bench.csv", "repo,files\nlinux,81902\n")
	writeFile(t, root, "bench.tsv", "repo\tfiles\nlinux\t81902\n")

	code, m := get(t, s, "/api/table?path=bench.csv")
	if code != 200 {
		t.Fatalf("status %d: %v", code, m)
	}
	rows, _ := m["rows"].([]any)
	if len(rows) != 1 || m["cols"] != float64(2) {
		t.Errorf("response = %v", m)
	}
	for path, want := range map[string]bool{"bench.csv": true, "bench.tsv": true, "main.go": false} {
		if _, m := get(t, s, "/api/file?path="+path); m["table"] != want {
			t.Errorf("%s: table = %v, want %v", path, m["table"], want)
		}
	}
	if code, _ := get(t, s, "/api/table?path=main.go"); code != 415 {
		t.Errorf("non-table file: status %d, want 415", code)
	}
}

// rowSource is an endless CSV body that counts the bytes read from it.
type rowSource struct {
	row  string
	off  int
	read int64
}

func (s *rowSource) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		c := copy(p[n:], s.row[s.off:])
		n += c
		s.off = (s.off + c) % len(s.row)
	}
	s.read += int64(n)
	return n, nil
}

func TestTableReadsNoMoreThanTheByteCap(t *testing.T) {
	src := &rowSource{row: strings.Repeat("y", 10000) + ",end\n"}
	res, err := renderTable(io.MultiReader(strings.NewReader("a,b\n"), src), 50<<20, ',')
	if err != nil {
		t.Fatal(err)
	}
	if src.read > maxTableBytes {
		t.Errorf("read %d bytes of a 50 MB file, cap is %d", src.read, maxTableBytes)
	}
	if !res.Truncated || len(res.Rows) == 0 {
		t.Fatalf("truncated %v with %d rows", res.Truncated, len(res.Rows))
	}
	for _, r := range res.Rows {
		if len(r.Cells) != 2 || r.Cells[1] != "end" {
			t.Fatalf("the record the byte cap cut through reached the result: %d cells", len(r.Cells))
		}
	}
}

func TestTableFileAtTheByteCapIsNotTruncated(t *testing.T) {
	src := "a,b\n1,2\n"
	res, err := renderTable(strings.NewReader(src), maxTableBytes, ',')
	if err != nil || res.Truncated {
		t.Errorf("truncated %v, err %v", res.Truncated, err)
	}
}

func TestTableDelimiterComesFromTheFileNotTheExtension(t *testing.T) {
	for name, tc := range map[string]struct {
		src      string
		fallback rune
		want     []string
	}{
		"tab-separated .csv":            {"date\tplace\tnote\n2018\tLondon\tx, y\n", ',', []string{"2018", "London", "x, y"}},
		"semicolon .csv":                {"a;b;c\n1,5;2;3\n", ',', []string{"1,5", "2", "3"}},
		"pipe .csv":                     {"a|b\n1|2\n", ',', []string{"1", "2"}},
		"comma .tsv":                    {"a,b\n1,2\n", '\t', []string{"1", "2"}},
		"quoted commas ignored":         {"\"a,b,c\"\tz\n1\t2\n", ',', []string{"1", "2"}},
		"blank first line":              {"\n\na\tb\n1\t2\n", ',', []string{"1", "2"}},
		"single column keeps extension": {"name\nx\n", ',', []string{"x"}},
	} {
		res, err := renderTable(strings.NewReader(tc.src), int64(len(tc.src)), tc.fallback)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(res.Rows) == 0 || !reflect.DeepEqual(res.Rows[0].Cells, tc.want) {
			t.Errorf("%s: rows = %+v, want first row %q", name, res.Rows, tc.want)
		}
	}
}

func TestTableTieBetweenSeparatorsKeepsTheExtension(t *testing.T) {
	if got := sniffDelim([]byte("a,b\tc\n"), '\t'); got != '\t' {
		t.Errorf("tie picked %q, want the .tsv tab", got)
	}
	if got := sniffDelim([]byte("a,b\tc\n"), ','); got != ',' {
		t.Errorf("tie picked %q, want the .csv comma", got)
	}
}

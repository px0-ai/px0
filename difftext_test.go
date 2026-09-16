package main

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// rowShape flattens a hunk's rows to "type:old:new" so a whole hunk can be
// compared in one line instead of field by field.
func rowShape(rows []DiffRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Type + ":" + lineNo(r.Old) + ":" + lineNo(r.New)
	}
	return out
}

// lineNo renders a row line number, or "-" for the side a row does not have.
func lineNo(n int) string {
	if n == 0 {
		return "-"
	}
	return strconv.Itoa(n)
}

func TestParseUnifiedDiff(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/x.go b/x.go",
		"index 1111111..2222222 100644",
		"--- a/x.go",
		"+++ b/x.go",
		"@@ -1,3 +1,3 @@ func main() {",
		" package main",
		"-old line",
		"+new line",
		" tail",
		"@@ -10,2 +10,3 @@",
		" ctx",
		"+extra",
		" more",
		"\\ No newline at end of file",
		"",
	}, "\n")

	hunks := parseUnifiedDiff(diff)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %d, want 2 (%+v)", len(hunks), hunks)
	}
	if hunks[0].OldStart != 1 || hunks[0].NewStart != 1 || hunks[0].Section != "func main() {" {
		t.Errorf("hunk 0 = %d/%d %q, want 1/1 %q", hunks[0].OldStart, hunks[0].NewStart, hunks[0].Section, "func main() {")
	}
	// The pre-hunk headers ("diff --git", "index", "---", "+++") must not be
	// mistaken for rows: "---" and "+++" start with - and + like real ones do.
	want := []string{"ctx:1:1", "del:2:-", "add:-:2", "ctx:3:3"}
	if got := rowShape(hunks[0].Rows); !reflect.DeepEqual(got, want) {
		t.Errorf("hunk 0 rows = %v, want %v", got, want)
	}
	if hunks[1].OldStart != 10 || hunks[1].NewStart != 10 || hunks[1].Section != "" {
		t.Errorf("hunk 1 = %d/%d %q, want 10/10 and no section", hunks[1].OldStart, hunks[1].NewStart, hunks[1].Section)
	}
	want = []string{"ctx:10:10", "add:-:11", "ctx:11:12"}
	if got := rowShape(hunks[1].Rows); !reflect.DeepEqual(got, want) {
		t.Errorf("hunk 1 rows = %v, want %v", got, want)
	}
	if got := hunks[0].Rows[1].text; got != "old line" {
		t.Errorf("row text = %q, want %q (the leading marker is stripped)", got, "old line")
	}

	if h := parseUnifiedDiff(""); h != nil {
		t.Errorf("empty diff = %v, want nil", h)
	}
	if h := parseUnifiedDiff("@@ garbage @@\n+x\n"); len(h) != 0 {
		t.Errorf("malformed header = %v, want no hunks", h)
	}
}

// A one-character edit must mark one character, not the whole line: the line
// colour already says the line changed, so the ranges only earn their place by
// being narrow.
func TestWordRangesSingleCharacterChange(t *testing.T) {
	dels, adds := wordRanges("\treturn a + b", "\treturn a - b")
	want := [][2]int{{10, 11}}
	if !reflect.DeepEqual(dels, want) || !reflect.DeepEqual(adds, want) {
		t.Errorf("ranges = %v / %v, want %v on both sides (the operator alone)", dels, adds, want)
	}

	if d, a := wordRanges("same", "same"); d != nil || a != nil {
		t.Errorf("identical lines = %v / %v, want no ranges", d, a)
	}

	// A line with nothing left in common is a rewrite; speckling it with
	// ranges would be noise, so it keeps only its line-level colour.
	if d, a := wordRanges("alpha bravo charlie", "zulu yankee xray"); d != nil || a != nil {
		t.Errorf("rewrite = %v / %v, want no ranges", d, a)
	}

	// Offsets count UTF-16 code units, which is what JavaScript string
	// indexing counts: an astral character is two.
	dels, _ = wordRanges("x = \"\U0001F600\" + a", "x = \"\U0001F600\" + b")
	want = [][2]int{{11, 12}} // not 10: the emoji is two units, and not a byte offset either
	if !reflect.DeepEqual(dels, want) {
		t.Errorf("astral ranges = %v, want %v (the emoji counts as two units)", dels, want)
	}
}

// The reason the rows are tokenised on the server: a hunk can cut a block
// comment or a raw string in half, and only lexing the whole side gets those
// lines right. Per-line lexing calls them code.
func TestDiffRowsLexesTheWholeSide(t *testing.T) {
	const oldSrc = "package main\n\n/*\nalpha beta\ngamma delta\n*/\n\nfunc main() {}\n"
	const newSrc = "package main\n\n/*\nalpha beta\ngamma DELTA\n*/\n\nfunc main() {}\n"
	diff := "@@ -5 +5 @@\n-gamma delta\n+gamma DELTA\n"

	hunks := diffRows("x.go", oldSrc, newSrc, diff)
	if len(hunks) != 1 || len(hunks[0].Rows) != 2 {
		t.Fatalf("hunks = %+v, want one hunk of two rows", hunks)
	}
	del, add := hunks[0].Rows[0], hunks[0].Rows[1]
	for _, r := range []DiffRow{del, add} {
		if !strings.Contains(r.HTML, "<i class=c>") {
			t.Errorf("%s row html = %q, want it lexed as a comment", r.Type, r.HTML)
		}
	}
	// The control: the same line lexed on its own is not a comment. Without
	// that difference the whole-side lexing would be buying nothing.
	lone := highlightLines(newDoc(newSrc, "x.go").lexer, "gamma DELTA", 1)[0]
	if strings.Contains(lone, "<i class=c>") {
		t.Fatal("the single-line control is already a comment; the test proves nothing")
	}
	if add.HTML == lone {
		t.Errorf("row html %q equals the single-line lexing; the side was not lexed whole", add.HTML)
	}
	// The word diff still ran over the pair.
	if len(add.Words) != 1 {
		t.Errorf("add words = %v, want one range for the changed word", add.Words)
	}
	// The text side is internal: it must not reach the wire.
	wire, err := json.Marshal(add)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	if strings.Contains(string(wire), `"text"`) {
		t.Errorf("the plain row text is being served: %s", wire)
	}
}

// A row whose line number is past the end of its side -- the file moved under
// us between the diff and the read -- must still come back tokenised, not empty.
func TestDiffRowsFallsBackWhenTheSideIsShort(t *testing.T) {
	diff := "@@ -1 +1 @@\n-gone\n+here\n"
	hunks := diffRows("x.go", "", "", diff)
	if len(hunks) != 1 || len(hunks[0].Rows) != 2 {
		t.Fatalf("hunks = %+v, want one hunk of two rows", hunks)
	}
	for _, r := range hunks[0].Rows {
		if r.HTML == "" {
			t.Errorf("%s row has no html; the per-line fallback did not run", r.Type)
		}
	}
}

// An untracked file has no HEAD side at all, so `git diff HEAD` says nothing
// about it. It is modelled as one hunk of pure additions instead.
func TestAddedFileHunks(t *testing.T) {
	hunks := addedFileHunks("x.go", "package main\n\nfunc main() {}\n")
	if len(hunks) != 1 {
		t.Fatalf("hunks = %d, want 1", len(hunks))
	}
	want := []string{"add:-:1", "add:-:2", "add:-:3"}
	if got := rowShape(hunks[0].Rows); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %v, want %v (the trailing newline is not a fourth line)", got, want)
	}
	if h := hunks[0]; h.NewStart != 1 || h.OldStart != 0 {
		t.Errorf("hunk starts = %d/%d, want 0/1", h.OldStart, h.NewStart)
	}
	if html := hunks[0].Rows[0].HTML; !strings.Contains(html, "<i class=k>") {
		t.Errorf("row html = %q, want the package keyword tokenised", html)
	}
	if addedFileHunks("x.go", "") != nil {
		t.Error("an empty file produced hunks")
	}
}

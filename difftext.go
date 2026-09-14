package main

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
)

// Row model for the diff view. The server ships rows that are already
// tokenised and already carry their intra-line word ranges, so the client only
// lays them out. Two reasons this lives here and not in JavaScript:
//
//   - Syntax highlighting needs whole-file lexer state. A block comment or a
//     raw string that a hunk cuts in half is lexed correctly only if the side
//     it belongs to is lexed end to end; per-line lexing gets it wrong exactly
//     where a diff is most interesting.
//   - The word-level diff is O(n*m) over a pair of lines. Doing it once per
//     request beats doing it in the render loop of every viewer.
//
// Nothing here touches the filesystem or git: both sides of the file arrive as
// strings from the caller.

// DiffRow is one line of a file's diff. Type is "ctx", "add" or "del"; Old and
// New are 1-based line numbers in the HEAD copy and the on-disk copy, present
// only on the sides where they exist.
//
// HTML is the tokenised line: a flat sequence of text and non-nested
// <i class=xx>...</i> spans, escaped with &amp; &lt; &gt; and nothing else.
// Words indexes the *plain* text of the line -- half-open [start, end) ranges
// in UTF-16 code units, which is what JavaScript string offsets count -- and
// marks the runs that actually changed within an otherwise similar line. The
// flat HTML shape is what lets the client slice at those offsets.
type DiffRow struct {
	Type  string   `json:"type"`
	Old   int      `json:"old,omitempty"`
	New   int      `json:"new,omitempty"`
	HTML  string   `json:"html"`
	Words [][2]int `json:"words,omitempty"`

	text string // plain source of the line; input to the word diff, never served
}

// DiffHunk is one @@ block. Section is the trailing context git puts after the
// header (usually the enclosing function).
type DiffHunk struct {
	OldStart int       `json:"oldStart"`
	NewStart int       `json:"newStart"`
	Section  string    `json:"section"`
	Rows     []DiffRow `json:"rows"`
}

// diffRows turns one file's unified diff into tokenised rows. oldSrc is the
// file as of HEAD and newSrc the file on disk; either may be empty (a file
// added, or one deleted), in which case that side falls back to lexing each
// row on its own.
func diffRows(rel, oldSrc, newSrc, diff string) []DiffHunk {
	hunks := parseUnifiedDiff(diff)
	if len(hunks) == 0 {
		return nil
	}
	markWords(hunks)
	tokeniseHunks(hunks, rel, oldSrc, newSrc)
	return hunks
}

// addedFileHunks models a file that has no HEAD side at all -- an untracked
// file -- as a single hunk of pure additions. `git diff HEAD` says nothing
// about untracked files, so without this they are invisible to review, which
// is the worst possible blind spot: a brand new file is the thing most worth
// reading.
func addedFileHunks(rel, src string) []DiffHunk {
	lines := splitSource(src)
	if len(lines) == 0 {
		return nil
	}
	rows := make([]DiffRow, len(lines))
	for i, l := range lines {
		rows[i] = DiffRow{Type: "add", New: i + 1, text: l}
	}
	hunks := []DiffHunk{{OldStart: 0, NewStart: 1, Rows: rows}}
	tokeniseHunks(hunks, rel, "", src)
	return hunks
}

// ---------------------------------------------------------------- parsing

// parseUnifiedDiff reads the output of `git diff` into hunks of rows carrying
// old- and/or new-file line numbers. Everything before the first @@ (the
// "diff --git", "index", "---" and "+++" headers) is dropped: the caller
// already knows which file this is.
func parseUnifiedDiff(text string) []DiffHunk {
	if text == "" {
		return nil
	}
	var hunks []DiffHunk
	cur := -1
	oldLine, newLine := 0, 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "@@") {
			hOld, hNew, section, ok := parseHunkHeader(line)
			if !ok {
				continue
			}
			oldLine, newLine = hOld, hNew
			hunks = append(hunks, DiffHunk{OldStart: hOld, NewStart: hNew, Section: section})
			cur = len(hunks) - 1
			continue
		}
		// Before the first hunk, the trailing artefact of the final split, and
		// git's "\ No newline at end of file" marker: none of them are rows.
		if cur < 0 || line == "" || strings.HasPrefix(line, "\\") {
			continue
		}
		body := line[1:]
		switch line[0] {
		case '+':
			hunks[cur].Rows = append(hunks[cur].Rows, DiffRow{Type: "add", New: newLine, text: body})
			newLine++
		case '-':
			hunks[cur].Rows = append(hunks[cur].Rows, DiffRow{Type: "del", Old: oldLine, text: body})
			oldLine++
		default:
			hunks[cur].Rows = append(hunks[cur].Rows, DiffRow{Type: "ctx", Old: oldLine, New: newLine, text: body})
			oldLine++
			newLine++
		}
	}
	return hunks
}

// parseHunkHeader reads "@@ -a,b +c,d @@ section" into its two start lines and
// the section text.
func parseHunkHeader(hdr string) (oldStart, newStart int, section string, ok bool) {
	rest, found := strings.CutPrefix(hdr, "@@ ")
	if !found {
		return 0, 0, "", false
	}
	ranges, tail, found := strings.Cut(rest, " @@")
	if !found {
		return 0, 0, "", false
	}
	oldR, newR, found := strings.Cut(ranges, " ")
	if !found {
		return 0, 0, "", false
	}
	oldStart, ok = parseRangeStart(oldR, '-')
	if !ok {
		return 0, 0, "", false
	}
	newStart, ok = parseRangeStart(newR, '+')
	if !ok {
		return 0, 0, "", false
	}
	return oldStart, newStart, strings.TrimPrefix(tail, " "), true
}

// parseRangeStart reads the "start" of a "-start,count" or "+start" range.
func parseRangeStart(r string, sign byte) (int, bool) {
	if len(r) < 2 || r[0] != sign {
		return 0, false
	}
	digits := r[1:]
	if i := strings.IndexByte(digits, ','); i >= 0 {
		digits = digits[:i]
	}
	n := 0
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if digits == "" {
		return 0, false
	}
	return n, true
}

// ------------------------------------------------------------ tokenisation

// tokeniseHunks fills in every row's HTML. Each side of the file is lexed
// whole and the resulting lines are handed out to the rows that reference
// them, so a construct spanning a hunk boundary is still lexed in context.
// A row whose side is missing (or whose line number is past the end of it,
// which means the file moved under us) falls back to lexing that one line.
func tokeniseHunks(hunks []DiffHunk, rel, oldSrc, newSrc string) {
	ref := newSrc
	if ref == "" {
		ref = oldSrc
	}
	// newDoc owns the lexer choice for a path; borrowing it here keeps the
	// diff view highlighting a file exactly the way the editor does.
	lexer := newDoc(ref, rel).lexer
	oldHTML := lexSide(lexer, oldSrc)
	newHTML := lexSide(lexer, newSrc)

	for i := range hunks {
		for j := range hunks[i].Rows {
			row := &hunks[i].Rows[j]
			var html string
			var ok bool
			if row.Type == "del" {
				html, ok = nthLine(oldHTML, row.Old)
			} else {
				html, ok = nthLine(newHTML, row.New)
			}
			if !ok {
				html = highlightLines(lexer, row.text, 1)[0]
			}
			row.HTML = html
		}
	}
}

func lexSide(lexer chroma.Lexer, src string) []string {
	if src == "" {
		return nil
	}
	return highlightLines(lexer, src, strings.Count(src, "\n")+1)
}

func nthLine(lines []string, n int) (string, bool) {
	if n < 1 || n > len(lines) {
		return "", false
	}
	return lines[n-1], true
}

// splitSource splits a file into lines, dropping the empty element a trailing
// newline leaves behind.
func splitSource(src string) []string {
	if src == "" {
		return nil
	}
	lines := strings.Split(src, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// -------------------------------------------------------------- word diff

const (
	// Above this many words on either side the quadratic pass is skipped and
	// the whole changed middle is marked as one range. Generated one-liners
	// exist; a diff request must not turn into a CPU stall.
	maxWordTokens = 400

	// A pair of lines that has almost nothing in common is not a tweak, it is
	// a rewrite. Marking ranges there speckles the row for no information, so
	// below this share of surviving characters the row keeps only its
	// line-level colour.
	minCommonShare = 0.25
)

// markWords pairs each run of deletions with the run of additions that
// immediately follows it, index by index, and computes the intra-line ranges
// for each pair. Rows with no partner (an unbalanced run) keep no ranges: the
// line as a whole is the change.
func markWords(hunks []DiffHunk) {
	for i := range hunks {
		rows := hunks[i].Rows
		for j := 0; j < len(rows); {
			if rows[j].Type != "del" {
				j++
				continue
			}
			delStart := j
			for j < len(rows) && rows[j].Type == "del" {
				j++
			}
			addStart := j
			for j < len(rows) && rows[j].Type == "add" {
				j++
			}
			pairs := min(addStart-delStart, j-addStart)
			for k := 0; k < pairs; k++ {
				del, add := &rows[delStart+k], &rows[addStart+k]
				del.Words, add.Words = wordRanges(del.text, add.text)
			}
		}
	}
}

// wtok is one word of a line: an identifier run, a whitespace run, or a single
// other rune, with its half-open offsets in UTF-16 code units.
type wtok struct {
	text  string
	start int
	end   int
}

// wordRanges diffs two lines word by word and returns the ranges that were
// removed from the first and inserted into the second.
func wordRanges(oldText, newText string) (dels, adds [][2]int) {
	if oldText == newText {
		return nil, nil
	}
	a, b := wordTokens(oldText), wordTokens(newText)

	// Trim the identical head and tail first: it is the common case, it is
	// linear, and it keeps the quadratic pass off most lines entirely.
	head := 0
	for head < len(a) && head < len(b) && a[head].text == b[head].text {
		head++
	}
	tail := 0
	for tail < len(a)-head && tail < len(b)-head &&
		a[len(a)-1-tail].text == b[len(b)-1-tail].text {
		tail++
	}
	am, bm := a[head:len(a)-tail], b[head:len(b)-tail]
	if len(am) == 0 && len(bm) == 0 {
		return nil, nil
	}

	common := utf16Len(oldText) - spanLen(am)
	if float64(common) < minCommonShare*float64(max(utf16Len(oldText), utf16Len(newText))) {
		return nil, nil // a rewrite, not an edit
	}

	if len(am) > maxWordTokens || len(bm) > maxWordTokens {
		return spanRange(am), spanRange(bm)
	}
	keepA, keepB := lcsKept(am, bm)
	return changedRanges(am, keepA), changedRanges(bm, keepB)
}

// wordTokens splits a line into identifier runs, whitespace runs and single
// other runes, recording each token's offsets in UTF-16 code units.
func wordTokens(s string) []wtok {
	var out []wtok
	off := 0
	runes := []rune(s)
	for i := 0; i < len(runes); {
		start, startOff := i, off
		switch {
		case isWordRune(runes[i]):
			for i < len(runes) && isWordRune(runes[i]) {
				off += utf16RuneLen(runes[i])
				i++
			}
		case runes[i] == ' ' || runes[i] == '\t':
			for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
				off += utf16RuneLen(runes[i])
				i++
			}
		default:
			off += utf16RuneLen(runes[i])
			i++
		}
		out = append(out, wtok{text: string(runes[start:i]), start: startOff, end: off})
	}
	return out
}

func isWordRune(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r > 0x7f
}

func utf16RuneLen(r rune) int {
	if r > 0xffff {
		return 2
	}
	return 1
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n += utf16RuneLen(r)
	}
	return n
}

// lcsKept marks, for each side, the tokens that belong to a longest common
// subsequence -- that is, the ones that survived the edit.
func lcsKept(a, b []wtok) (keepA, keepB []bool) {
	keepA, keepB = make([]bool, len(a)), make([]bool, len(b))
	w := len(b) + 1
	table := make([]int, (len(a)+1)*w)
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i].text == b[j].text {
				table[i*w+j] = table[(i+1)*w+j+1] + 1
			} else {
				table[i*w+j] = max(table[(i+1)*w+j], table[i*w+j+1])
			}
		}
	}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i].text == b[j].text:
			keepA[i], keepB[j] = true, true
			i++
			j++
		case table[(i+1)*w+j] >= table[i*w+j+1]:
			i++
		default:
			j++
		}
	}
	return keepA, keepB
}

// changedRanges merges the runs of non-surviving tokens into offset ranges.
func changedRanges(toks []wtok, keep []bool) [][2]int {
	var out [][2]int
	for i := 0; i < len(toks); {
		if keep[i] {
			i++
			continue
		}
		start := toks[i].start
		end := toks[i].end
		i++
		for i < len(toks) && !keep[i] {
			end = toks[i].end
			i++
		}
		out = append(out, [2]int{start, end})
	}
	return out
}

// spanRange collapses a whole token run into a single range.
func spanRange(toks []wtok) [][2]int {
	if len(toks) == 0 {
		return nil
	}
	return [][2]int{{toks[0].start, toks[len(toks)-1].end}}
}

func spanLen(toks []wtok) int {
	if len(toks) == 0 {
		return 0
	}
	return toks[len(toks)-1].end - toks[0].start
}

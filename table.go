package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// The table view builds every row it shows as DOM, so it stops early
	// instead of reading the whole file: opening a 50 MB CSV costs the same
	// as opening one at the cap. 1,000 rows is the code view's own chunk
	// size, and at that size a table paints as fast as the source view does.
	// The source view still has every line.
	maxTableRows  = 1000
	maxTableBytes = 1 << 20
)

// tableDelim reports whether rel opens as a table, and the separator its
// extension implies. The file's own first line can overrule it (sniffDelim).
func tableDelim(rel string) (rune, bool) {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".csv":
		return ',', true
	case ".tsv":
		return '\t', true
	}
	return 0, false
}

// isTable reports whether rel opens in the table view.
func isTable(rel string) bool {
	_, ok := tableDelim(rel)
	return ok
}

type tableRow struct {
	Line  int      `json:"line"`
	Cells []string `json:"cells"`
}

type tableResult struct {
	Header     []string   `json:"header"`
	HeaderLine int        `json:"headerLine"`
	Rows       []tableRow `json:"rows"`
	Cols       int        `json:"cols"`
	Truncated  bool       `json:"truncated"`
}

// renderTable reads at most maxTableBytes of r, a file of size bytes, and up
// to maxTableRows records after the header. fallback is the separator the
// extension implies. When the byte cap stops short of the end of the file,
// the last record read may be partial: it is dropped and the result is marked
// truncated.
//
// Each row carries the source line its first cell starts on, which is how the
// table stays in step with the source view, find and go-to-line when a quoted
// cell spans lines or blank lines are skipped.
func renderTable(r io.Reader, size int64, fallback rune) (tableResult, error) {
	cut := size > maxTableBytes
	br := bufio.NewReaderSize(io.LimitReader(r, maxTableBytes), 64<<10)
	head, _ := br.Peek(64 << 10) // whatever is there; a short file returns less
	cr := csv.NewReader(br)
	cr.Comma = sniffDelim(head, fallback)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	res := tableResult{Header: []string{}, Rows: []tableRow{}}

	rec, err := cr.Read()
	if err == io.EOF {
		return res, nil
	}
	if err != nil && !cut {
		return res, err
	}
	if rec != nil {
		rec[0] = strings.TrimPrefix(rec[0], "\uFEFF")
		res.Header = rec
		res.HeaderLine, _ = cr.FieldPos(0)
		res.Cols = len(rec)
	}

	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			if cut {
				break // most likely the record the byte cap cut through
			}
			return res, err
		}
		if len(res.Rows) == maxTableRows {
			res.Truncated = true
			return res, nil
		}
		line, _ := cr.FieldPos(0)
		res.Rows = append(res.Rows, tableRow{Line: line, Cells: rec})
		res.Cols = max(res.Cols, len(rec))
	}
	if cut {
		if n := len(res.Rows); n > 0 {
			res.Rows = res.Rows[:n-1]
		}
		res.Truncated = true
	}
	return res, nil
}

// handleTable serves a CSV or TSV file parsed into rows for the table view.
// Cells go back as plain strings; the client inserts them as text, never HTML.
func (s *Server) handleTable(w http.ResponseWriter, r *http.Request) {
	abs, rel, ok := s.resolvePath(r.URL.Query().Get("path"))
	if !ok {
		fail(w, 400, "bad path")
		return
	}
	delim, ok := tableDelim(rel)
	if !ok {
		fail(w, 415, "not a CSV or TSV file")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		fail(w, 404, err.Error())
		return
	}
	if st.IsDir() {
		fail(w, 415, "is a directory")
		return
	}
	res, err := renderTable(f, st.Size(), delim)
	if err != nil {
		var pe *csv.ParseError
		if errors.As(err, &pe) {
			fail(w, 422, err.Error())
			return
		}
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, res)
}

// sniffDelim picks the separator the first non-blank line uses most, counting
// only outside quotes: comma, tab, semicolon or pipe. Files are misnamed often
// enough (a tab-separated .csv, a semicolon .csv from a European spreadsheet)
// that the extension is only the tie-breaker.
func sniffDelim(head []byte, fallback rune) rune {
	line := head
	for len(line) > 0 {
		i := bytes.IndexByte(line, '\n')
		if i < 0 {
			break
		}
		if strings.TrimSpace(string(line[:i])) != "" {
			line = line[:i]
			break
		}
		line = line[i+1:]
	}
	counts := map[rune]int{}
	quoted := false
	for _, c := range string(line) {
		switch {
		case c == '"':
			quoted = !quoted
		case !quoted && (c == ',' || c == '\t' || c == ';' || c == '|'):
			counts[c]++
		}
	}
	best, n := fallback, counts[fallback]
	for _, c := range []rune{',', '\t', ';', '|'} {
		if counts[c] > n {
			best, n = c, counts[c]
		}
	}
	return best
}

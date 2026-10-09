package table

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"io"
	"path/filepath"
	"strings"
)

const (
	MaxRows  = 1000
	MaxBytes = 1 << 20
)

// Delim reports whether rel opens as a table, and the separator its
// extension implies. The file's own first line can overrule it (SniffDelim).
func Delim(rel string) (rune, bool) {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".csv":
		return ',', true
	case ".tsv":
		return '\t', true
	}
	return 0, false
}

// IsTable reports whether rel opens in the table view.
func IsTable(rel string) bool {
	_, ok := Delim(rel)
	return ok
}

type Row struct {
	Line  int      `json:"line"`
	Cells []string `json:"cells"`
}

type Result struct {
	Header     []string `json:"header"`
	HeaderLine int      `json:"headerLine"`
	Rows       []Row    `json:"rows"`
	Cols       int      `json:"cols"`
	Truncated  bool     `json:"truncated"`
}

// Render reads at most MaxBytes of r, a file of size bytes, and up
// to MaxRows records after the header. fallback is the separator the
// extension implies. When the byte cap stops short of the end of the file,
// the last record read may be partial: it is dropped and the result is marked
// truncated.
func Render(r io.Reader, size int64, fallback rune) (Result, error) {
	cut := size > MaxBytes
	br := bufio.NewReaderSize(io.LimitReader(r, MaxBytes), 64<<10)
	head, _ := br.Peek(64 << 10) // whatever is there; a short file returns less
	cr := csv.NewReader(br)
	cr.Comma = SniffDelim(head, fallback)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	res := Result{Header: []string{}, Rows: []Row{}}

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
		if len(res.Rows) == MaxRows {
			res.Truncated = true
			return res, nil
		}
		line, _ := cr.FieldPos(0)
		res.Rows = append(res.Rows, Row{Line: line, Cells: rec})
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

// SniffDelim picks the separator the first non-blank line uses most, counting
// only outside quotes: comma, tab, semicolon or pipe. Files are misnamed often
// enough (a tab-separated .csv, a semicolon .csv from a European spreadsheet)
// that the extension is only the tie-breaker.
func SniffDelim(head []byte, fallback rune) rune {
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

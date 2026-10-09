package main

import (
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"os"

	itbl "px0/internal/table"
)

const (
	maxTableRows  = itbl.MaxRows
	maxTableBytes = itbl.MaxBytes
)

func tableDelim(rel string) (rune, bool) {
	return itbl.Delim(rel)
}

func isTable(rel string) bool {
	return itbl.IsTable(rel)
}

type tableRow = itbl.Row
type tableResult = itbl.Result

func renderTable(r io.Reader, size int64, fallback rune) (tableResult, error) {
	return itbl.Render(r, size, fallback)
}

func sniffDelim(head []byte, fallback rune) rune {
	return itbl.SniffDelim(head, fallback)
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

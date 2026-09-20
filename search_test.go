package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchContextCancellation(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 20; i++ {
		content := "package main\n\nfunc SearchTarget() {}\n"
		if err := os.WriteFile(filepath.Join(root, "file_"+string(rune('a'+i))+".go"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ix := NewIndex(root)
	ix.Build()

	// 1. Normal search should find all files
	res, _, err := SearchContext(context.Background(), ix, SearchOpts{Query: "SearchTarget"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res) != 20 {
		t.Fatalf("expected 20 file hits, got %d", len(res))
	}

	// 2. Search with already canceled context returns immediately
	ctxCanceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = SearchContext(ctxCanceled, ix, SearchOpts{Query: "SearchTarget"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// 3. Search cancelled while in-flight terminates worker goroutines
	ctxTimed, cancelTimed := context.WithTimeout(context.Background(), 10*time.Microsecond)
	defer cancelTimed()
	_, _, err = SearchContext(ctxTimed, ix, SearchOpts{Query: "SearchTarget"})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected cancellation or deadline error, got %v", err)
	}
}

func TestHandleSearchWithCanceledRequest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(root)
	ix.Build()
	s := NewServer(ix, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/search?q=Hello", nil)
	ctx, cancel := context.WithCancel(req.Context())
	cancel() // cancel immediately to simulate aborted request
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	s.handleSearch(rec, req)

	// Since context was canceled, handleSearch should return cleanly without writing a 400 error
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("expected canceled search to return quietly, got status %d", rec.Code)
	}
}

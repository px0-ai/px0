package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"v0.1.0", "0.1.0", 0},
		{"0.1.0", "v0.1.0", 0},
		{"0.2.0", "0.1.0", 1},
		{"0.1.0", "0.2.0", -1},
		{"1.0.0", "0.9.9", 1},
		{"0.1.1", "0.1.0", 1},
		{"0.10.0", "0.9.0", 1},
		{"0.1.0-alpha", "0.1.0", 0},
		{"0.2.0", "0.1.99", 1},
	}

	for _, tt := range tests {
		got := compareSemver(tt.v1, tt.v2)
		if got != tt.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
		}
	}
}

func TestFetchLatestRelease(t *testing.T) {
	fakeRelease := githubRelease{
		TagName: "v0.2.0",
		Name:    "px0 v0.2.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{
				Name:               "px0-0.2.0-linux-amd64",
				BrowserDownloadURL: "https://example.com/download/px0",
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fakeRelease)
	}))
	defer server.Close()

	t.Setenv("PX0_UPDATE_URL", server.URL)

	rel, err := fetchLatestRelease("test/repo")
	if err != nil {
		t.Fatalf("fetchLatestRelease failed: %v", err)
	}

	if rel.TagName != "v0.2.0" {
		t.Errorf("got TagName %q, want v0.2.0", rel.TagName)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != "px0-0.2.0-linux-amd64" {
		t.Errorf("unexpected assets: %+v", rel.Assets)
	}
}

func TestUpdateStatePersistence(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	statePath := filepath.Join(tmpDir, "px0", "update_check.json")
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected state file to not exist yet")
	}

	now := time.Now().Truncate(time.Second)
	s := &updateState{
		LastChecked: now,
		LatestVer:   "0.2.0",
	}
	writeUpdateState(s)

	readState, err := readUpdateState()
	if err != nil {
		t.Fatalf("readUpdateState failed: %v", err)
	}

	if readState.LatestVer != "0.2.0" {
		t.Errorf("read LatestVer = %q, want 0.2.0", readState.LatestVer)
	}
	if readState.LastChecked.Unix() != now.Unix() {
		t.Errorf("read LastChecked = %v, want %v", readState.LastChecked, now)
	}
}

func TestDownloadVerifiedAsset(t *testing.T) {
	assetName := "px0-0.2.0-linux-amd64"
	binary := []byte("test binary")
	digest := fmt.Sprintf("%x", sha256.Sum256(binary))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + assetName:
			_, _ = w.Write(binary)
		case "/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var got bytes.Buffer
	err := downloadVerifiedAsset(server.Client(), server.URL+"/"+assetName, server.URL+"/checksums.txt", assetName, &got)
	if err != nil {
		t.Fatalf("downloadVerifiedAsset failed: %v", err)
	}
	if !bytes.Equal(got.Bytes(), binary) {
		t.Fatalf("downloaded %q, want %q", got.Bytes(), binary)
	}
}

func TestDownloadVerifiedAssetRejectsMismatch(t *testing.T) {
	assetName := "px0-0.2.0-linux-amd64"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums.txt" {
			_, _ = fmt.Fprintf(w, "%064x  %s\n", 0, assetName)
			return
		}
		_, _ = w.Write([]byte("tampered binary"))
	}))
	defer server.Close()

	err := downloadVerifiedAsset(server.Client(), server.URL+"/"+assetName, server.URL+"/checksums.txt", assetName, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("got %v, want checksum mismatch", err)
	}
}

func TestChecksumForRejectsMissingAndMalformedEntries(t *testing.T) {
	assetName := "px0-0.2.0-linux-amd64"
	for name, checksums := range map[string]string{
		"missing":   fmt.Sprintf("%064x  other-asset\n", 0),
		"malformed": "not-a-sha256  " + assetName + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := checksumFor([]byte(checksums), assetName); err == nil {
				t.Fatal("expected checksum validation to fail")
			}
		})
	}
}

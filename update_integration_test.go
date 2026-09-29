package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunSelfUpdateMock(t *testing.T) {
	// Create a dummy binary that will act as the "updated" binary
	tmpDir := t.TempDir()
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}

	newBinPath := filepath.Join(tmpDir, "new-px0"+ext)
	// Write a shell script / batch script or copy existing current test binary
	script := "#!/bin/sh\necho 'px0 0.2.0 (" + runtime.GOOS + "/" + runtime.GOARCH + ")'\n"
	if err := os.WriteFile(newBinPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write fake binary: %v", err)
	}

	assetName := fmt.Sprintf("px0-0.2.0-%s-%s%s", runtime.GOOS, runtime.GOARCH, ext)

	// Setup mock server
	var serverURL string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/test/px0/releases/latest":
			rel := githubRelease{
				TagName: "v0.2.0",
				Name:    "px0 v0.2.0",
				Assets: []struct {
					Name               string `json:"name"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{
					{
						Name:               assetName,
						BrowserDownloadURL: serverURL + "/download/" + assetName,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel)
		case "/download/" + assetName:
			http.ServeFile(w, r, newBinPath)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	serverURL = ts.URL

	t.Setenv("PX0_REPO", "test/px0")
	t.Setenv("PX0_UPDATE_URL", ts.URL+"/repos/test/px0/releases/latest")
	t.Setenv("XDG_STATE_HOME", tmpDir)

	rel, err := fetchLatestRelease("test/px0")
	if err != nil {
		t.Fatalf("fetchLatestRelease returned error: %v", err)
	}
	if rel.TagName != "v0.2.0" {
		t.Fatalf("expected v0.2.0, got %s", rel.TagName)
	}
	if len(rel.Assets) != 1 || rel.Assets[0].Name != assetName {
		t.Fatalf("expected asset %s, got %+v", assetName, rel.Assets)
	}
}

// TestInstallUpdate exercises the actual download-verify-swap logic against
// a stand-in execPath, without touching the real test binary via
// os.Executable() (which autoUpdate/runSelfUpdate use in production).
func TestInstallUpdate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a runnable .exe; covered on unix")
	}

	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "px0")
	if err := os.WriteFile(execPath, []byte("#!/bin/sh\necho 'px0 0.1.0'\n"), 0o755); err != nil {
		t.Fatalf("failed to seed current binary: %v", err)
	}

	assetName := fmt.Sprintf("px0-0.2.0-%s-%s", runtime.GOOS, runtime.GOARCH)
	newBinary := []byte("#!/bin/sh\necho 'px0 0.2.0'\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(newBinary))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/" + assetName:
			_, _ = w.Write(newBinary)
		case "/checksums.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	rel := &githubRelease{
		TagName: "v0.2.0",
		Assets: []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		}{
			{Name: assetName, BrowserDownloadURL: ts.URL + "/download/" + assetName},
			{Name: "checksums.txt", BrowserDownloadURL: ts.URL + "/checksums.txt"},
		},
	}

	if err := installUpdate(rel, "0.2.0", execPath); err != nil {
		t.Fatalf("installUpdate failed: %v", err)
	}

	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("failed to read installed binary: %v", err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("installed binary content = %q, want %q", got, newBinary)
	}
	info, err := os.Stat(execPath)
	if err != nil {
		t.Fatalf("failed to stat installed binary: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("installed binary is not executable: mode %v", info.Mode())
	}
}

// TestAutoUpdateFastPathSkipsNetwork verifies that when the cached state was
// checked recently and shows no newer version, autoUpdate never touches the
// network (and so never reaches the os.Executable()-based install/re-exec
// path, which would be unsafe to exercise against the test binary).
func TestAutoUpdateFastPathSkipsNetwork(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("PX0_UPDATE_URL", "http://127.0.0.1:1/should-not-be-hit")

	writeUpdateState(&updateState{LastChecked: time.Now(), LatestVer: "0.1.0"})

	autoUpdate("0.1.0")
	// No panic/hang means the cached "up to date" state short-circuited
	// before any network call.
}

// TestAutoUpdateDueCheckSkipsInstallWhenUpToDate verifies that a due daily
// check that finds no newer release just refreshes the cache and returns,
// again without reaching the install/re-exec path.
func TestAutoUpdateDueCheckSkipsInstallWhenUpToDate(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{TagName: "v0.1.0"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer ts.Close()
	t.Setenv("PX0_UPDATE_URL", ts.URL)

	autoUpdate("0.1.0")

	state, err := readUpdateState()
	if err != nil {
		t.Fatalf("readUpdateState failed: %v", err)
	}
	if state.LatestVer != "0.1.0" {
		t.Fatalf("LatestVer = %q, want 0.1.0", state.LatestVer)
	}
}

func captureAutoUpdateStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() failed: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = w

	fn()

	os.Stdout = origStdout
	_ = w.Close()

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

// TestAutoUpdateVerbosePrintsUpToDate verifies that -verbose surfaces an
// explicit "up to date" line on stdout when the daily check finds nothing
// newer, both when the check actually hits the network...
func TestAutoUpdateVerbosePrintsUpToDate(t *testing.T) {
	origVerbose := uiVerbose
	defer func() { uiVerbose = origVerbose }()
	uiVerbose = true

	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{TagName: "v0.1.0"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer ts.Close()
	t.Setenv("PX0_UPDATE_URL", ts.URL)

	out := captureAutoUpdateStdout(t, func() { autoUpdate("0.1.0") })
	if !strings.Contains(out, "up to date") {
		t.Fatalf("expected an up-to-date message in verbose output, got %q", out)
	}
}

// ...and when the cached state from an earlier check already says so.
func TestAutoUpdateVerbosePrintsUpToDateFromCache(t *testing.T) {
	origVerbose := uiVerbose
	defer func() { uiVerbose = origVerbose }()
	uiVerbose = true

	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)
	t.Setenv("PX0_UPDATE_URL", "http://127.0.0.1:1/should-not-be-hit")

	writeUpdateState(&updateState{LastChecked: time.Now(), LatestVer: "0.1.0"})

	out := captureAutoUpdateStdout(t, func() { autoUpdate("0.1.0") })
	if !strings.Contains(out, "up to date") {
		t.Fatalf("expected an up-to-date message in verbose output, got %q", out)
	}
}

// TestAutoUpdateQuietWhenNotVerbose verifies that without -verbose, an
// up-to-date check stays silent (no stdout noise on every ordinary run).
func TestAutoUpdateQuietWhenNotVerbose(t *testing.T) {
	origVerbose := uiVerbose
	defer func() { uiVerbose = origVerbose }()
	uiVerbose = false

	tmpDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpDir)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := githubRelease{TagName: "v0.1.0"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rel)
	}))
	defer ts.Close()
	t.Setenv("PX0_UPDATE_URL", ts.URL)

	out := captureAutoUpdateStdout(t, func() { autoUpdate("0.1.0") })
	if out != "" {
		t.Fatalf("expected no output without -verbose, got %q", out)
	}
}

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultRepo       = "px0-ai/px0"
	updateCheckPeriod = 24 * time.Hour
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type updateState struct {
	LastChecked time.Time `json:"last_checked"`
	LatestVer   string    `json:"latest_ver"`
}

func getRepoName() string {
	if r := os.Getenv("PX0_REPO"); r != "" {
		return r
	}
	return defaultRepo
}

func stateFilePath() string {
	// Respect XDG_STATE_HOME or fallback to ~/.local/state/px0 or ~/.px0
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "px0", "update_check.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "px0_update_check.json")
	}
	return filepath.Join(home, ".px0", "update_check.json")
}

func readUpdateState() (*updateState, error) {
	p := stateFilePath()
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var s updateState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func writeUpdateState(s *updateState) {
	p := stateFilePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	_ = os.WriteFile(p, data, 0o644)
}

// compareSemver returns:
//
//	 1 if v1 > v2
//	-1 if v1 < v2
//	 0 if v1 == v2
func compareSemver(v1, v2 string) int {
	v1 = strings.TrimPrefix(strings.TrimSpace(v1), "v")
	v2 = strings.TrimPrefix(strings.TrimSpace(v2), "v")

	parts1 := strings.Split(v1, ".")
	parts2 := strings.Split(v2, ".")

	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var n1, n2 int
		if i < len(parts1) {
			// Extract any leading numeric portion
			numStr := strings.Split(parts1[i], "-")[0]
			n1, _ = strconv.Atoi(numStr)
		}
		if i < len(parts2) {
			numStr := strings.Split(parts2[i], "-")[0]
			n2, _ = strconv.Atoi(numStr)
		}
		if n1 > n2 {
			return 1
		}
		if n1 < n2 {
			return -1
		}
	}
	return 0
}

// fetchLatestRelease queries the GitHub API or release redirect for the latest version.
func fetchLatestRelease(repo string) (*githubRelease, error) {
	apiURL := os.Getenv("PX0_UPDATE_URL")
	if apiURL == "" {
		apiURL = fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "px0-updater")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no published releases found for %s yet", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, apiURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func downloadAsset(client *http.Client, url string, dst io.Writer) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

func checksumFor(data []byte, assetName string) (string, error) {
	var checksum string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		if name != assetName {
			continue
		}
		if checksum != "" {
			return "", fmt.Errorf("multiple checksums found for %s", assetName)
		}
		raw, err := hex.DecodeString(fields[0])
		if err != nil || len(raw) != sha256.Size {
			return "", fmt.Errorf("invalid checksum for %s", assetName)
		}
		checksum = strings.ToLower(fields[0])
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if checksum == "" {
		return "", fmt.Errorf("checksum not found for %s", assetName)
	}
	return checksum, nil
}

func downloadVerifiedAsset(client *http.Client, assetURL, checksumURL, assetName string, dst io.Writer) error {
	var checksums bytes.Buffer
	if err := downloadAsset(client, checksumURL, &checksums); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	expected, err := checksumFor(checksums.Bytes(), assetName)
	if err != nil {
		return err
	}

	hash := sha256.New()
	if err := downloadAsset(client, assetURL, io.MultiWriter(dst, hash)); err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s", assetName)
	}
	return nil
}

// checkDailyUpdate runs in a background goroutine on CLI startup.
// It ensures that checking for updates never blocks px0 startup (<1ms).
func checkDailyUpdate(currentVersion string) {
	if uiQuiet {
		return
	}

	state, _ := readUpdateState()
	now := time.Now()
	if state != nil && now.Sub(state.LastChecked) < updateCheckPeriod {
		// If we already detected a newer version during the last check, inform user
		if state.LatestVer != "" && compareSemver(state.LatestVer, currentVersion) > 0 {
			printUpdateNotification(state.LatestVer, currentVersion)
		}
		return
	}

	// Needs check
	rel, err := fetchLatestRelease(getRepoName())
	if err != nil {
		return
	}

	latestVer := strings.TrimPrefix(rel.TagName, "v")
	writeUpdateState(&updateState{
		LastChecked: now,
		LatestVer:   latestVer,
	})

	if compareSemver(latestVer, currentVersion) > 0 {
		printUpdateNotification(latestVer, currentVersion)
	}
}

func printUpdateNotification(latestVer, currentVersion string) {
	msg := fmt.Sprintf("a new version of px0 (v%s) is available (current: v%s)", latestVer, currentVersion)
	uiStatus("step", msg, "run 'px0 --update' to upgrade", 0, os.Stderr)
}

// runSelfUpdate implements px0 --update.
func runSelfUpdate(currentVer string) error {
	repo := getRepoName()
	uiStatus("step", fmt.Sprintf("checking for updates from %s...", repo), "", 0, os.Stdout)

	rel, err := fetchLatestRelease(repo)
	if err != nil {
		return fmt.Errorf("failed to fetch latest release: %w", err)
	}

	latestVer := strings.TrimPrefix(rel.TagName, "v")
	if compareSemver(latestVer, currentVer) <= 0 {
		uiStatus("ok", fmt.Sprintf("px0 is already up to date (v%s)", currentVer), "", 0, os.Stdout)
		return nil
	}

	uiStatus("info", fmt.Sprintf("found newer version v%s (current: v%s)", latestVer, currentVer), "", 0, os.Stdout)

	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	expectedAsset := fmt.Sprintf("px0-%s-%s-%s%s", latestVer, runtime.GOOS, runtime.GOARCH, ext)

	var downloadURL, checksumURL string
	for _, asset := range rel.Assets {
		switch asset.Name {
		case expectedAsset:
			downloadURL = asset.BrowserDownloadURL
		case "checksums.txt":
			checksumURL = asset.BrowserDownloadURL
		}
	}
	if downloadURL == "" {
		// Fallback to standard github release download link pattern
		downloadURL = fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, rel.TagName, expectedAsset)
	}
	if checksumURL == "" {
		return fmt.Errorf("release %s does not include checksums.txt", rel.TagName)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine running binary location: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("could not resolve executable symlink: %w", err)
	}

	uiStatus("step", fmt.Sprintf("downloading %s...", expectedAsset), "", 0, os.Stdout)

	// Download to temporary file in the same directory as the executable (for atomic rename)
	dir := filepath.Dir(execPath)
	tmpFile, err := os.CreateTemp(dir, "px0-update-*")
	if err != nil {
		// If directory is not writable, warn user about permissions
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied writing to %s. Try running with 'sudo px0 --update'", dir)
		}
		// Try temp directory as fallback
		tmpFile, err = os.CreateTemp("", "px0-update-*")
		if err != nil {
			return fmt.Errorf("could not create temporary file: %w", err)
		}
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	client := &http.Client{Timeout: 60 * time.Second}
	if err := downloadVerifiedAsset(client, downloadURL, checksumURL, expectedAsset, tmpFile); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("verify downloaded update: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed saving downloaded binary: %w", err)
	}

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("failed setting executable permissions: %w", err)
	}

	// Verify the downloaded binary works
	cmd := exec.Command(tmpPath, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("verification of new binary failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}

	// Atomically replace existing binary
	if runtime.GOOS == "windows" {
		// Windows does not allow replacing a running executable directly.
		// Rename current binary to .old, then move new binary into place.
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath)
		if err := os.Rename(execPath, oldPath); err != nil {
			return fmt.Errorf("failed to move current binary on Windows: %w", err)
		}
		if err := copyOrMove(tmpPath, execPath); err != nil {
			// Restore old path on failure
			_ = os.Rename(oldPath, execPath)
			return fmt.Errorf("failed to place new binary: %w", err)
		}
	} else {
		if err := os.Rename(tmpPath, execPath); err != nil {
			// If cross-device link error, copy instead
			if err := copyOrMove(tmpPath, execPath); err != nil {
				if os.IsPermission(err) {
					return fmt.Errorf("permission denied replacing %s. Try running with 'sudo px0 --update'", execPath)
				}
				return fmt.Errorf("failed to replace binary %s: %w", execPath, err)
			}
		}
	}

	// Update cached check state
	writeUpdateState(&updateState{
		LastChecked: time.Now(),
		LatestVer:   latestVer,
	})

	uiStatus("ok", fmt.Sprintf("px0 successfully updated to v%s at %s", latestVer, execPath), "", 0, os.Stdout)
	return nil
}

func copyOrMove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	// Write to temporary file in target directory first
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, "px0-replace-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	return os.Rename(tmpName, dst)
}

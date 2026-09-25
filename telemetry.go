package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// posthogKey can be injected at compile time:
// go build -ldflags="-X main.posthogKey=phc_..."
var posthogKey string

const (
	defaultPostHogHost = "https://us.i.posthog.com"
	telemetryQueueSize = 128
)

type telemetryEvent struct {
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties"`
}

// TelemetryService provides non-blocking, anonymous usage telemetry.
// Events are buffered in a queue and sent asynchronously to prevent delaying startup or UI responsiveness.
// No file names, code contents, commit messages, or personal identifiable information are ever collected.
type TelemetryService struct {
	enabled    bool
	apiKey     string
	host       string
	distinctID string
	sessionID  string
	startTime  time.Time
	queue      chan telemetryEvent
	wg         sync.WaitGroup
	quit       chan struct{}
	client     *http.Client
	closeOnce  sync.Once
}

// isOptedOut checks common opt-out indicators:
// - CLI flag --no-telemetry
// - Environment variable DO_NOT_TRACK=1
// - Environment variable PX0_TELEMETRY=0 / false / off / no
func isOptedOut(flagNoTelemetry bool) bool {
	if flagNoTelemetry {
		return true
	}
	if os.Getenv("DO_NOT_TRACK") == "1" {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PX0_TELEMETRY")))
	if v == "0" || v == "false" || v == "off" || v == "no" {
		return true
	}
	return false
}

// filesBucket maps file count to coarse size tiers to preserve privacy
// while enabling meaningful monorepo vs small project analysis.
func filesBucket(n int) string {
	switch {
	case n >= 100000:
		return ">100k"
	case n >= 50000:
		return "50k-100k"
	case n >= 10000:
		return "10k-50k"
	case n >= 2500:
		return "2.5k-10k"
	case n >= 500:
		return "500-2.5k"
	case n >= 100:
		return "100-500"
	default:
		return "<100"
	}
}

// getOrGenerateDistinctID retrieves or initializes a persistent anonymous UUID.
// Saved to ~/.px0/anonymous_id. If writing fails, an ephemeral ID is returned.
func getOrGenerateDistinctID() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		idPath := filepath.Join(home, ".px0", "anonymous_id")
		if data, err := os.ReadFile(idPath); err == nil {
			id := strings.TrimSpace(string(data))
			if len(id) >= 16 {
				return id
			}
		}
	}

	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("px0-%d", time.Now().UnixNano())
	}
	id := hex.EncodeToString(b)

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dir := filepath.Join(home, ".px0")
		if err := os.MkdirAll(dir, 0755); err == nil {
			_ = os.WriteFile(filepath.Join(dir, "anonymous_id"), []byte(id), 0644)
		}
	}

	return id
}

// generateUUID returns a cryptographically random RFC 4122 v4 UUID string.
func generateUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// NewTelemetryService creates and starts a background telemetry worker.
func NewTelemetryService(flagNoTelemetry bool) *TelemetryService {
	key := strings.TrimSpace(posthogKey)
	if envKey := strings.TrimSpace(os.Getenv("PX0_POSTHOG_KEY")); envKey != "" {
		key = envKey
	}

	host := strings.TrimRight(strings.TrimSpace(os.Getenv("PX0_POSTHOG_HOST")), "/")
	if host == "" {
		host = defaultPostHogHost
	}

	enabled := key != "" && !isOptedOut(flagNoTelemetry)

	t := &TelemetryService{
		enabled:    enabled,
		apiKey:     key,
		host:       host,
		distinctID: getOrGenerateDistinctID(),
		sessionID:  generateUUID(),
		startTime:  time.Now(),
		queue:      make(chan telemetryEvent, telemetryQueueSize),
		quit:       make(chan struct{}),
		client: &http.Client{
			Timeout: 4 * time.Second,
		},
	}

	if enabled {
		t.wg.Add(1)
		go t.worker()
	}

	return t
}

// Track enqueues an event to be dispatched asynchronously.
// If telemetry is disabled or queue is full, it drops immediately without blocking.
func (t *TelemetryService) Track(event string, props map[string]any) {
	if t == nil || !t.enabled {
		return
	}

	if props == nil {
		props = make(map[string]any)
	}

	// Enrich with standard PostHog environment and session properties
	props["distinct_id"] = t.distinctID
	props["$session_id"] = t.sessionID
	props["$lib"] = "px0"
	props["$lib_version"] = version
	props["$os"] = runtime.GOOS
	props["$arch"] = runtime.GOARCH

	evt := telemetryEvent{
		Event:      event,
		Properties: props,
	}

	select {
	case t.queue <- evt:
	default:
		// Drop if buffer full to preserve zero latency
	}
}

// Close emits session_ended and session_stopped with total active duration.
// The stop event is dispatched synchronously so process exit cannot kill it before arrival.
func (t *TelemetryService) Close(reason string) {
	if t == nil || !t.enabled {
		return
	}
	t.closeOnce.Do(func() {
		durationSec := int(time.Since(t.startTime).Seconds())
		if durationSec < 0 {
			durationSec = 0
		}

		// Close quit channel and wait for any in-flight background events to drain
		close(t.quit)
		t.wg.Wait()

		// Dispatch session_ended and session_stopped synchronously
		props := map[string]any{
			"distinct_id":      t.distinctID,
			"$session_id":      t.sessionID,
			"$lib":             "px0",
			"$lib_version":     version,
			"$os":              runtime.GOOS,
			"$arch":            runtime.GOARCH,
			"duration_seconds": durationSec,
			"exit_reason":      reason,
		}

		t.send(telemetryEvent{
			Event:      "session_ended",
			Properties: props,
		})
		t.send(telemetryEvent{
			Event:      "session_stopped",
			Properties: props,
		})
	})
}

func (t *TelemetryService) worker() {
	defer t.wg.Done()

	for {
		select {
		case <-t.quit:
			// Drain remaining events in queue
			for {
				select {
				case evt := <-t.queue:
					t.send(evt)
				default:
					return
				}
			}
		case evt := <-t.queue:
			t.send(evt)
		}
	}
}

func (t *TelemetryService) send(evt telemetryEvent) {
	debug := os.Getenv("PX0_TELEMETRY_DEBUG") == "1"

	payload := map[string]any{
		"api_key":    t.apiKey,
		"event":      evt.Event,
		"properties": evt.Properties,
		"timestamp":  time.Now().UTC().Format(time.RFC3339Nano),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[telemetry] marshal error: %v\n", err)
		}
		return
	}

	req, err := http.NewRequest("POST", t.host+"/capture/", bytes.NewReader(body))
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[telemetry] request error: %v\n", err)
		}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "px0/"+version)

	resp, err := t.client.Do(req)
	if err != nil {
		if debug {
			fmt.Fprintf(os.Stderr, "[telemetry] send %s failed: %v\n", evt.Event, err)
		}
		return
	}
	_ = resp.Body.Close()

	if debug {
		fmt.Fprintf(os.Stderr, "[telemetry] sent %s -> %s (%s)\n", evt.Event, t.host, resp.Status)
	}
}

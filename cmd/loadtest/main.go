package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/joho/godotenv"
)

type loadTestResult struct {
	duration time.Duration
	success  bool
	err      error
}

func main() {
	// Parse CLI Flags
	concurrency := flag.Int("concurrency", 10, "Number of concurrent workers")
	durationSec := flag.Int("duration", 5, "Load test duration in seconds")
	apiEndpoint := flag.String("endpoint", "http://localhost:8000", "Target API Server base URL")
	dbURLFlag := flag.String("db", "", "PostgreSQL database connection URL")
	flag.Parse()

	// Load .env if present
	_ = godotenv.Load()

	// Resolve Database URL
	dbURL := *dbURLFlag
	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		dbURL = "postgres://px0:px0secret@localhost:5432/px0?sslmode=disable"
	}

	ctx := context.Background()

	// 1. Connect to Database
	fmt.Printf("Connecting to database: %s\n", maskDSN(dbURL))
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		fmt.Printf("Error: failed to connect to database: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close(ctx)

	// 2. Setup Mock Data
	runID := strings.ReplaceAll(uuid.New().String(), "-", "")[:12]
	fmt.Printf("Creating temporary mock data with run ID: %s...\n", runID)

	orgID := uuid.New()
	orgName := "loadtest-org-" + runID
	_, err = conn.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1, $2)", orgID, orgName)
	if err != nil {
		fmt.Printf("Error: failed to create mock organization: %v\n", err)
		os.Exit(1)
	}

	teamID := uuid.New()
	teamName := "loadtest-team-" + runID
	_, err = conn.Exec(ctx, "INSERT INTO teams (id, name, org_id) VALUES ($1, $2, $3)", teamID, teamName, orgID)
	if err != nil {
		_, _ = conn.Exec(ctx, "DELETE FROM organizations WHERE id = $1", orgID)
		fmt.Printf("Error: failed to create mock team: %v\n", err)
		os.Exit(1)
	}

	rawKey := "ak_loadtest_" + strings.ReplaceAll(uuid.New().String(), "-", "")
	hashBytes := sha256.Sum256([]byte(rawKey))
	keyHash := fmt.Sprintf("%x", hashBytes)
	apiKeyID := uuid.New()
	_, err = conn.Exec(ctx, "INSERT INTO api_keys (id, name, key_prefix, key_hash, org_id, operation) VALUES ($1, $2, $3, $4, $5, $6)",
		apiKeyID, "loadtest-key-"+runID, "ak_loadtest", keyHash, orgID, "read_render")
	if err != nil {
		cleanUp(ctx, conn, orgID, teamID, uuid.Nil, uuid.Nil, uuid.Nil)
		fmt.Printf("Error: failed to create mock API key: %v\n", err)
		os.Exit(1)
	}

	_, err = conn.Exec(ctx, "INSERT INTO api_key_teams (api_key_id, team_id) VALUES ($1, $2)", apiKeyID, teamID)
	if err != nil {
		cleanUp(ctx, conn, orgID, teamID, apiKeyID, uuid.Nil, uuid.Nil)
		fmt.Printf("Error: failed to associate API key with team: %v\n", err)
		os.Exit(1)
	}

	promptID := uuid.New()
	promptName := "loadtest-prompt-" + runID
	promptSlug := "loadtest_prompt_" + runID
	_, err = conn.Exec(ctx, "INSERT INTO prompts (id, name, description, team_id, slug) VALUES ($1, $2, $3, $4, $5)",
		promptID, promptName, "Mock prompt for load testing", teamID, promptSlug)
	if err != nil {
		cleanUp(ctx, conn, orgID, teamID, apiKeyID, uuid.Nil, uuid.Nil)
		fmt.Printf("Error: failed to create mock prompt: %v\n", err)
		os.Exit(1)
	}

	versionID := uuid.New()
	templateStr := "Hello {{.name}}! Benchmark version rendering. Status is: {{.status}}."
	_, err = conn.Exec(ctx, "INSERT INTO prompt_versions (id, prompt_id, version, template, status) VALUES ($1, $2, $3, $4, $5)",
		versionID, promptID, 1, templateStr, "live")
	if err != nil {
		cleanUp(ctx, conn, orgID, teamID, apiKeyID, promptID, uuid.Nil)
		fmt.Printf("Error: failed to create mock live prompt version: %v\n", err)
		os.Exit(1)
	}

	// Defer final cleanup of all entities
	defer cleanUp(ctx, conn, orgID, teamID, apiKeyID, promptID, versionID)

	targetURL := fmt.Sprintf("%s/v1/prompts/%s/render", *apiEndpoint, promptSlug)
	fmt.Printf("Target live prompt render endpoint: %s\n", targetURL)

	// 3. Pre-flight Check
	preflightClient := &http.Client{Timeout: 3 * time.Second}
	payloadMap := map[string]any{
		"variables": map[string]any{
			"name":   "LoadtestUser",
			"status": "active",
		},
	}
	payloadBytes, _ := json.Marshal(payloadMap)
	req, _ := http.NewRequest("POST", targetURL, bytes.NewBuffer(payloadBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", rawKey)

	resp, err := preflightClient.Do(req)
	if err != nil {
		fmt.Printf("\nPre-flight check failed! Is the Go server running on %s?\n", *apiEndpoint)
		fmt.Printf("Ensure the application is running (`make dev` or `docker compose up -d`) and try again.\n")
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("\nPre-flight check failed with HTTP status code: %d\n", resp.StatusCode)
		return
	}
	fmt.Printf("Pre-flight check succeeded! Running benchmark for %ds with concurrency of %d...\n\n", *durationSec, *concurrency)

	// 4. Execute Benchmark
	testCtx, cancel := context.WithTimeout(context.Background(), time.Duration(*durationSec)*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	allResults := make([][]loadTestResult, *concurrency)
	startTime := time.Now()

	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			client := &http.Client{
				Timeout: 5 * time.Second,
				Transport: &http.Transport{
					MaxIdleConnsPerHost: *concurrency,
				},
			}
			var results []loadTestResult

			for {
				select {
				case <-testCtx.Done():
					allResults[workerID] = results
					return
				default:
					reqPayload := bytes.NewBuffer(payloadBytes)
					req, err := http.NewRequestWithContext(testCtx, "POST", targetURL, reqPayload)
					if err != nil {
						results = append(results, loadTestResult{success: false, err: err})
						continue
					}
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-API-Key", rawKey)

					reqStart := time.Now()
					resp, err := client.Do(req)
					duration := time.Since(reqStart)

					if err != nil {
						results = append(results, loadTestResult{duration: duration, success: false, err: err})
						continue
					}

					if resp.StatusCode == http.StatusOK {
						results = append(results, loadTestResult{duration: duration, success: true})
					} else {
						results = append(results, loadTestResult{duration: duration, success: false, err: fmt.Errorf("HTTP %d", resp.StatusCode)})
					}
					resp.Body.Close()
				}
			}
		}(i)
	}

	wg.Wait()
	actualDuration := time.Since(startTime)

	// 5. Gather and Process Statistics
	var totalReqs int64
	var successReqs int64
	var failedReqs int64
	var successfulDurations []time.Duration
	var errMap = make(map[string]int64)

	for _, workerResults := range allResults {
		for _, res := range workerResults {
			totalReqs++
			if res.success {
				successReqs++
				successfulDurations = append(successfulDurations, res.duration)
			} else {
				failedReqs++
				if res.err != nil {
					errMap[res.err.Error()]++
				} else {
					errMap["unknown_error"]++
				}
			}
		}
	}

	if totalReqs == 0 {
		fmt.Println("No requests completed during benchmark.")
		return
	}

	// Latency percentiles calculation
	sort.Slice(successfulDurations, func(i, j int) bool {
		return successfulDurations[i] < successfulDurations[j]
	})

	var avgDuration time.Duration
	var p50, p90, p95, p99 time.Duration

	if len(successfulDurations) > 0 {
		var sumDurations int64
		for _, d := range successfulDurations {
			sumDurations += d.Nanoseconds()
		}
		avgDuration = time.Duration(sumDurations / int64(len(successfulDurations)))

		p50 = percentile(successfulDurations, 50)
		p90 = percentile(successfulDurations, 90)
		p95 = percentile(successfulDurations, 95)
		p99 = percentile(successfulDurations, 99)
	}

	rps := float64(totalReqs) / actualDuration.Seconds()
	successRate := (float64(successReqs) / float64(totalReqs)) * 100

	// 6. Print Benchmark Table
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.Debug)
	fmt.Fprintln(w, "Metric\tValue")
	fmt.Fprintln(w, "------\t-----")
	fmt.Fprintf(w, "Concurrency\t%d workers\n", *concurrency)
	fmt.Fprintf(w, "Benchmark Duration\t%.2fs\n", actualDuration.Seconds())
	fmt.Fprintf(w, "Total Requests\t%d\n", totalReqs)
	fmt.Fprintf(w, "Successful Requests\t%d\n", successReqs)
	fmt.Fprintf(w, "Failed Requests\t%d\n", failedReqs)
	fmt.Fprintf(w, "Request Throughput (RPS)\t%.2f reqs/s\n", rps)
	fmt.Fprintf(w, "Success Rate\t%.2f%%\n", successRate)
	fmt.Fprintf(w, "Average Latency\t%v\n", avgDuration)
	fmt.Fprintf(w, "p50 (Median) Latency\t%v\n", p50)
	fmt.Fprintf(w, "p90 Latency\t%v\n", p90)
	fmt.Fprintf(w, "p95 Latency\t%v\n", p95)
	fmt.Fprintf(w, "p99 Latency\t%v\n", p99)
	w.Flush()

	if len(errMap) > 0 {
		fmt.Println("\nError Breakdown:")
		for errMsg, count := range errMap {
			fmt.Printf("- %s: %d occurrences\n", errMsg, count)
		}
	}
	fmt.Println()
}

func percentile(durations []time.Duration, p int) time.Duration {
	if len(durations) == 0 {
		return 0
	}
	idx := (p * len(durations)) / 100
	if idx >= len(durations) {
		idx = len(durations) - 1
	}
	return durations[idx]
}

func maskDSN(dsn string) string {
	parts := strings.Split(dsn, "@")
	if len(parts) <= 1 {
		return dsn
	}
	subParts := strings.Split(parts[0], "://")
	if len(subParts) <= 1 {
		return dsn
	}
	// Redact password from connection string
	credParts := strings.Split(subParts[1], ":")
	if len(credParts) > 1 {
		return fmt.Sprintf("%s://%s:****@%s", subParts[0], credParts[0], parts[1])
	}
	return dsn
}

func cleanUp(ctx context.Context, conn *pgx.Conn, orgID, teamID, apiKeyID, promptID, versionID uuid.UUID) {
	fmt.Println("Cleaning up temporary mock data...")
	if versionID != uuid.Nil {
		_, _ = conn.Exec(ctx, "DELETE FROM prompt_versions WHERE id = $1", versionID)
	}
	if promptID != uuid.Nil {
		_, _ = conn.Exec(ctx, "DELETE FROM prompts WHERE id = $1", promptID)
	}
	if apiKeyID != uuid.Nil {
		_, _ = conn.Exec(ctx, "DELETE FROM api_key_teams WHERE api_key_id = $1", apiKeyID)
		_, _ = conn.Exec(ctx, "DELETE FROM api_keys WHERE id = $1", apiKeyID)
	}
	if teamID != uuid.Nil {
		_, _ = conn.Exec(ctx, "DELETE FROM team_members WHERE team_id = $1", teamID)
		_, _ = conn.Exec(ctx, "DELETE FROM teams WHERE id = $1", teamID)
	}
	if orgID != uuid.Nil {
		_, _ = conn.Exec(ctx, "DELETE FROM organizations WHERE id = $1", orgID)
	}
	fmt.Println("Cleanup complete!")
}

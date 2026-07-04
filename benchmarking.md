# Benchmarking and Performance Guide

This guide describes how to benchmark the px0 Go application, evaluate response latencies under concurrent load, and monitor resource consumption using the built-in observability stack.

## Overview

The px0 Go application includes a self-contained load testing tool designed to evaluate the throughput and latency of the prompt rendering pipeline. The tool performs safe, transactional database writes to establish transient test records, sends high-concurrency requests to the target API server, computes latency percentiles, and restores the database state upon completion.

## Prerequisites

Ensure the following local systems are running before executing any benchmarks:

- The Go compiler (version 1.21 or later)
- Docker and Docker Compose
- The target PostgreSQL database, Redis instance, and Go API server

To boot all required background services, run:

```bash
docker compose up -d
```

Confirm that the Go API server is running and listening on port 8000:

```bash
curl -i http://localhost:8000/v1/health
```

For more details on setting up local services, see the [Getting Started Guide](get-started.md). For detailed setup instructions on accessing Prometheus or Grafana, see the [Telemetry and Benchmarking Guide](get-started-telemetry.md).

## Running the Benchmark

The load testing utility is located at `cmd/loadtest/main.go`. You can run it directly using the Go toolchain.

### Configuration Flags

The tool accepts several command-line flags to control the test parameters:

- `-concurrency`: The number of concurrent worker goroutines. The default value is 10.
- `-duration`: The total duration of the benchmark run in seconds. The default value is 5.
- `-endpoint`: The base URL of the target Go API server. The default value is `http://localhost:8000`.
- `-db`: The connection URL for the target PostgreSQL database. If omitted, the tool attempts to use the `DATABASE_URL` environment variable, defaulting to `postgres://px0:px0secret@localhost:5432/px0?sslmode=disable` if not found.

### Execution Command

To execute a benchmark run with 20 concurrent workers for 10 seconds, run the following command from the project root:

```bash
go run cmd/loadtest/main.go -concurrency 20 -duration 10
```

### Lifecycle Actions

When you execute the load test script, the tool performs the following actions automatically:

- Establishes a connection to the PostgreSQL database specified in your environment or via the flags.
- Redacts sensitive credentials in the terminal logs when printing the database connection parameters.
- Creates transient test records including a mock organization, a team, a scoped API key, a parent prompt, and a live prompt template version.
- Executes a pre-flight request to the prompt rendering API using the newly generated API key to verify that the API server is reachable and active.
- Spawns parallel worker goroutines that send concurrent HTTP POST requests to the `/v1/prompts/{id}/render` endpoint.
- Accumulates response durations and success states in thread-safe memory buffers.
- Restores the database to its original state by removing all transient test records in a cleanup phase.
- Calculates and displays latency percentiles and throughput figures.

## Interpreting Benchmark Results

After completing a benchmark run, the tool outputs a structured table containing the performance metrics:

```
Metric                   | Value
------                   | -----
Concurrency              | 20 workers
Benchmark Duration       | 10.00s
Total Requests           | 24531
Successful Requests      | 24531
Failed Requests          | 0
Request Throughput (RPS) | 2453.10 reqs/s
Success Rate             | 100.00%
Average Latency          | 8.12ms
p50 (Median) Latency     | 7.21ms
p90 Latency              | 12.04ms
p95 Latency              | 15.67ms
p99 Latency              | 22.18ms
```

### Metric Definitions

The output table reports the following metrics:

- Concurrency: The number of simultaneous worker goroutines actively sending requests during the test.
- Benchmark Duration: The actual elapsed time of the load test run in seconds.
- Total Requests: The combined count of all requests completed by all workers.
- Successful Requests: The count of requests that received a 200 OK HTTP response.
- Failed Requests: The count of requests that failed due to network timeouts, connection drops, or non-200 HTTP response codes.
- Request Throughput (RPS): The average number of requests completed per second across the entire run.
- Success Rate: The percentage of total requests that succeeded.
- Average Latency: The arithmetic mean of all successful request durations.
- p50 (Median) Latency: The 50th percentile latency. Half of the requests completed faster than this duration.
- p90 Latency: The 90th percentile latency. 90% of all requests completed faster than this duration.
- p95 Latency: The 95th percentile latency. 95% of all requests completed faster than this duration.
- p99 Latency: The 99th percentile latency. 99% of all requests completed faster than this duration. This metric represents tail latency.

### Error Analysis

If any requests fail during execution, the tool displays an error breakdown section below the results table listing each distinct error message and its total occurrence count.

## Monitoring Performance via Telemetry

The px0 Go application emits telemetry data that can be analyzed concurrently during benchmark execution to assess system health.

### Prometheus Metrics

While the load test is running, open the Prometheus interface at http://localhost:9090 to inspect performance metrics:

- Run `px0_http_server_requests_total` to observe the total count of HTTP requests processed by the application, partitioned by route, method, and response status code.
- Run `px0_go_goroutine_count` to inspect active goroutine count and ensure the application runtime does not leak resources under sustained concurrency.

### Grafana Dashboards

Open the Grafana dashboard at http://localhost:3000 and locate the px0 Service Dashboard within the px0 folder. Key visualizations to monitor during a benchmark run include:

- HTTP Request Rate (RPS): Charts the real-time throughput of the application.
- HTTP Latency (p95 and p99): visualizes tail latencies to identify performance degradation.
- Active and In-flight Requests: Measures concurrent request load currently handled by the Go web server.
- System Resources: Monitors host and container resource consumption to determine physical hardware bottlenecks.

## Best Practices for Benchmarking

Follow these guidelines to ensure accurate and reproducible benchmarking results:

- Run the target Go server and the load test script on separate physical machines or dedicated virtual environments to prevent resource competition.
- Ensure the operating system limits for open files and network connections are set sufficiently high before initiating high-concurrency tests.
- Execute a brief, low-concurrency warm-up run before executing final benchmark runs to allow the Go runtime scheduler, database cache, and connection pools to stabilize.
- Run benchmarks multiple times and calculate the median results to account for transient network or host operating system resource fluctuations.

# Telemetry, Observability, and Benchmarking Guide

This guide provides step-by-step instructions to configure the end-to-end telemetry stack for `px0`, monitor system performance under load, and visualize real-time metrics for all system components (`Go API Server`, `PostgreSQL` database, and `Redis` cache) using `Prometheus` and `Grafana`.

## Observability Architecture

The `px0` stack uses standard cloud-native observability tools. Metrics from all core layers flow into `Prometheus` and are visualized in `Grafana` dashboards:

- `Go API` Application Metrics: Emitted using the OpenTelemetry SDK via OTLP (gRPC) to the `otel-collector`, which exposes a Prometheus scraper endpoint.
- `PostgreSQL` Database Metrics: Captured by the `postgres-exporter` which queries database statistics and exposes standard metrics.
- `Redis` Caching Metrics: Captured by the `redis-exporter` which collects keyspace, memory, and connection statistics from Redis.
- `Prometheus` Server: Scrapes the `otel-collector`, `postgres-exporter`, and `redis-exporter` at high frequency (5s intervals) to assemble a unified telemetry database.
- `Grafana` Platform: Pulls metrics from `Prometheus` to display preconfigured charts.

## Port Mapping and Web UIs

When you boot the stack, the following services and endpoints are exposed locally:

| Service | Port | Endpoint URL | Description |
| --- | --- | --- | --- |
| Go API Server | `8000` | http://localhost:8000 | The primary Go backend application built with Fiber |
| Prometheus | `9090` | http://localhost:9090 | The metrics database scraping all collector endpoints |
| Grafana | `3000` | http://localhost:3000 | The visualization platform pre-loaded with provisioned dashboards |
| OpenTelemetry Collector | `4317` / `4318` | localhost:4317 | The OTLP gRPC/HTTP receiver pipelines |
| OTEL Prometheus Exporter | `8889` | http://localhost:8889 | Scraper endpoint for Go API server metrics |
| PostgreSQL Exporter | `9187` | http://localhost:9187 | Scraper endpoint for PostgreSQL database metrics |
| Redis Exporter | `9121` | http://localhost:9121 | Scraper endpoint for Redis cache metrics |

## Step 1: Spin Up the Telemetry Stack

All services, exporters, and databases are orchestrated inside the `docker-compose.yml` file.

1. Ensure your local environment is configured (`.env` file created).
2. Boot all background containers in detached mode:

```bash
docker compose up -d
```

This starts the `postgres` and `redis` services, the `postgres-exporter` and `redis-exporter` instances, the `otel-collector` and `prometheus` metrics processing pipelines, and the Go application and `grafana` services.

## Step 2: Verify Prometheus Scraping Targets

Ensure that `Prometheus` is scraping all system components:

1. Open the Prometheus targets dashboard at http://localhost:9090/targets.
2. Verify that all three endpoints show a status of UP:
  - `otel-collector` (Go application metrics)
  - `postgres-exporter` (PostgreSQL database metrics)
  - `redis-exporter` (Redis cache metrics)

### Quick Sanity Queries in Prometheus

Navigate to http://localhost:9090 and execute these queries in the expression browser to confirm metric capture:

- App Metrics: Type `px0_http_server_requests_total` or `px0_go_goroutine_count` and click Execute.
- PostgreSQL Metrics: Type `pg_up` or `pg_stat_database_numbackends` and click Execute.
- Redis Metrics: Type `redis_up` or `redis_connected_clients` and click Execute.

## Step 3: Generate Load and Run Benchmarks

To see real-time metrics on your dashboards, you must simulate system load. The project contains a high-performance, self-contained load-testing utility located at `cmd/loadtest/main.go`.

### Running the Load Test

Run the benchmark script from the root directory of the project:

```bash
go run cmd/loadtest/main.go -concurrency 20 -duration 15
```

### What the Load Test Does Autonomously

The script performs the following actions:

- Connects directly to the PostgreSQL database.
- Registers a temporary test organization, team, scoped API Key, prompt, and a live prompt template version.
- Conducts a pre-flight sanity check on the rendering API endpoint.
- Launches concurrent worker goroutines that send high-throughput parallel POST requests to render the live prompt template.
- Safely cleans up and truncates all temporary test records upon completion.
- Measures response latency and success throughput, outputting an aligned console table.

### Sample Benchmark Results

```
Metric                    |Value
------                    |-----
Concurrency               |20 workers
Benchmark Duration        |15.00s
Total Requests            |14812
Successful Requests       |14792
Failed Requests           |20
Request Throughput (RPS)  |987.43 reqs/s
Success Rate              |99.86%
Average Latency           |20.19323ms
p50 (Median) Latency      |18.434648ms
p90 Latency               |25.100565ms
p95 Latency               |35.121596ms
p99 Latency               |41.767094ms
```

## Step 4: Setting Up Grafana Dashboards

Grafana is preconfigured to run locally with zero manual login overhead. Anonymous access is enabled with Admin permissions by default.

### Grafana Access

1. URL: http://localhost:3000
2. Default Login: Anonymous login is active. If prompted, the default administrative credentials are Username: `admin` / Password: `admin`.
3. Open Grafana at http://localhost:3000, navigate to Dashboards, open the `px0` folder.

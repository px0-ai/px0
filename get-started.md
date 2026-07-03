# Getting Started and Telemetry Guide

This guide provides instructions to spin up the local services using Docker Compose, send test requests to the Go API server, and inspect the collected metrics in Prometheus and Grafana.

## Prerequisites

Ensure Docker and Docker Compose are installed on your machine.

## Initial Setup

1. Clone the repository and navigate to the project root directory.
2. Copy the example environment file to create your local environment file:

```bash
cp .env.example .env
```

3. Open `.env` and set `RESEND_API_KEY=mock`. Setting this environment variable to `mock` bypasses email verification during user registration, automatically marking newly registered users as verified immediately without generating or printing a code. (Note that for other flows—like password resets or resending verification codes—setting it to `mock` or leaving it empty will generate and print the six-digit code to standard output). If you leave `RESEND_API_KEY` empty during registration, the Go API server will generate a six-digit verification code and print it to standard output, requiring you to retrieve it from the container logs and verify manually.

## Orchestration Services

Start all services in detached mode:

```bash
docker compose up -d
```

This command builds the Go application image, migrates the database, and boots up all dependency containers.

## Port Mapping and Web UIs

The following table lists the endpoints and interfaces exposed by the orchestrated services:

| Service | Port | Endpoint URL | Description |
| --- | --- | --- | --- |
| Go API Server | 8000 | http://localhost:8000 | The primary Go backend application built with Fiber |
| Prometheus | 9090 | http://localhost:9090 | The metrics database scraping the OpenTelemetry Collector |
| Grafana | 3000 | http://localhost:3000 | The visualization platform pre-loaded with dashboards |
| PostgreSQL | 5432 | localhost:5432 | The main database storing users, sessions, and configurations |
| Redis | 6379 | localhost:6379 | The caching and key-value store database |
| OpenTelemetry Collector | 4317 | localhost:4317 | The gRPC receiver pipeline for application metrics |

## Verification and Traffic Generation

Generate traffic to verify the end-to-end telemetry pipeline.

### Step 1: Health Check Request

Send a request to the health endpoint to confirm the API server is healthy:

```bash
curl -i http://localhost:8000/v1/health
```

### Step 2: Register a User

Register a new user using a JSON payload. Ensure your password complies with the complexity constraints: at least 8 characters, one uppercase letter, one lowercase letter, one digit, and one special character.

```bash
curl -i -X POST http://localhost:8000/v1/auth/register \
  -H "Content-Type: application/json" \
  -d '{"email": "hello@example.com", "password": "SecurePassword123!"}'
```

If you configured `RESEND_API_KEY=mock` in your environment file, the user is verified immediately, and you can skip the manual verification step below. 

If you left `RESEND_API_KEY` empty, retrieve the six-digit code from the container logs:

```bash
docker compose logs app | grep EMAIL
```

Then, submit the code to verify your account:

```bash
curl -i -X POST http://localhost:8000/v1/auth/verify-email \
  -H "Content-Type: application/json" \
  -d '{"email": "hello@example.com", "code": "<retrieved_code>"}'
```

### Step 3: Login to Obtain a Token

Log in to establish a session and obtain your bearer token:

```bash
curl -i -X POST http://localhost:8000/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email": "hello@example.com", "password": "SecurePassword123!"}'
```

Copy the token value from the JSON response to use in authenticated requests.

### Step 4: Create and Render Your First Prompt with a Programmatic API Key (Token)

With your session token (which starts with `sess_`), you can now make authenticated requests to configure your prompt infrastructure.

```bash
export PX0_ACCESS_TOKEN=<token>
```

#### 1. Retrieve Your Organization and Team IDs
We will query your self profile to find your organization and team IDs.

##### Get Organization ID

```bash
curl -s -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  http://localhost:8000/v1/me/orgs
```

```bash
export PX0_ORG_ID=org-id
```

##### Get Team ID:

```bash
curl -s -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  http://localhost:8000/v1/me/teams
```

```bash
export PX0_TEAM_ID=team-id
```

#### 2. Create a Programmatic API Key (Token)
Create a programmatic key with `all` or `read_render` operations. This acts as a machine/application token for rendering templates programmatically:

```bash
curl -i -X POST "http://localhost:8000/v1/api-keys" \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  --data @- <<EOF
{
  "name": "my-application-key",
  "org_id": "${PX0_ORG_ID}",
  "team_ids": ["${PX0_TEAM_ID}"],
  "operation": "all"
}
EOF
```

```bash
export PX0_API_KEY=api-key
```

#### 3. Create a Prompt

Create a prompt container under your team. Note that passing a `slug` is optional; if omitted, the API will automatically generate and normalize a slug from the prompt's `name` (e.g. "Greeting Prompt" becomes `greeting_prompt`). Here, we explicitly define the slug as `greeting`:

```bash
curl -i -X POST http://localhost:8000/v1/teams/${PX0_TEAM_ID}/prompts \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name": "Greeting Prompt", "slug": "greeting"}'
```

```bash
export PX0_PROMPT_ID=prompt-id
```

#### 4. Create a Prompt Version (Template)

Create a draft template version ([template syntax](https://docs.px0.ai/template-syntax)):

```bash
curl -i -X POST http://localhost:8000/v1/prompts/${PX0_PROMPT_ID}/versions \
  -H "Authorization: Bearer ${PX0_ACCESS_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"template": "Hello, {{.name}}! Welcome to px0."}'
```

Note: We are making a note of integer version number and not prompt version ID below.

```bash
export PX0_PROMPT_VERSION_NUM=1
```

#### 5. Render Your Prompt Template
Now, render your template by providing variables. You can render any version directly (even in draft status) or promote it to live and hit the live render endpoint.

##### Option A: Render a specific version directly (works on drafts)
Use your programmatic API key (or session token) to render version 1:

```bash
curl -i -X POST http://localhost:8000/v1/prompts/greeting/versions/${PX0_PROMPT_VERSION_NUM}/render \
  -H "Authorization: Bearer ${PX0_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"variables": {"name": "Alice"}}'
```

##### Option B: Promote the version and render the live endpoint
First, promote your version twice to move it along the lifecycle: `draft` -> `stable` -> `live`:

```bash
# Promote from draft to stable
curl -i -X POST http://localhost:8000/v1/prompts/${PX0_PROMPT_ID}/versions/${PX0_PROMPT_VERSION_NUM}/promote \
  -H "Authorization: Bearer ${PX0_API_KEY}"

# Promote from stable to live
curl -i -X POST http://localhost:8000/v1/prompts/${PX0_PROMPT_ID}/versions/${PX0_PROMPT_VERSION_NUM}/promote \
  -H "Authorization: Bearer ${PX0_API_KEY}"
```

Once live, anyone with the API key can render the current live prompt template without specifying a version number:

```bash
curl -i -X POST http://localhost:8000/v1/prompts/greeting/render \
  -H "Authorization: Bearer ${PX0_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"variables": {"name": "Bob"}}'
```

---

## Telemetry and Visualization

The application emits metrics using the OpenTelemetry SDK. The metrics flow to the OpenTelemetry Collector, which exposes them on port 8889 for Prometheus to scrape.

### Generating Repeat Requests for Metrics

Before exploring metrics in dashboards, simulate continuous traffic by sending multiple request loops to the health check or render endpoints to populate the telemetry database:

```bash
for i in {1..20}; do curl -s http://localhost:8000/v1/health > /dev/null; sleep 0.1; done
```

### Exploring Metrics in Prometheus

Navigate to the Prometheus Expression Browser at http://localhost:9090.

1. In the search bar, type `px0_http_server_requests_total` and click Execute. This displays the running total of HTTP requests.
2. Type `px0_go_goroutine_count` to inspect active Go routine usage in the runtime container.
3. Switch to the Graph tab to see these metrics charted over time.

### Visualizing in Grafana

Navigate to Grafana at http://localhost:3000. Anonymous login is enabled with Admin privileges by default, meaning you do not need credentials to access the dashboards.

1. Click on Dashboards in the left navigation menu.
2. Select the px0 folder.
3. Click on px0 Service Dashboard.
4. You will see pre-configured dashboards visualizing:
   - HTTP Request Rate (RPS) broken down by method, route, and status code
   - HTTP Latency (95th and 99th Percentile)
   - Active and In-Flight Requests
   - Go Goroutines count

## Benchmarking the Render API

The repository includes a self-contained Go loadtesting script located at `cmd/loadtest/main.go`. This script automatically handles database provisioning, concurrent API evaluation, and lock-free metric collection.

### Running the Load Test

1. Ensure the application and its database are running (e.g. `docker compose up -d`).
2. Run the load test script from the root directory:

```bash
go run cmd/loadtest/main.go -concurrency 20 -duration 10
```

The script performs the following actions autonomously:

- Establishes a connection to the PostgreSQL database.
- Registers a temporary organization, team, scoped API Key, prompt, and a live prompt template version.
- Validates the rendering endpoint connection through a pre-flight request.
- Launches concurrent workers executing parallel `POST` requests to render the live prompt.
- Gathers duration metrics for each individual request.
- Safely cleans up all generated database entities.
- Formats and displays latency and throughput metrics in a table.

### Example Output

When executed, the script prints an aligned console table similar to the following:

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

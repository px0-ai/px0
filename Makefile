.PHONY: install dev run build test test-store test-handler test-coverage lint format vet check docker-up docker-down

install:
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

dev:
	go run ./cmd/server

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

# Run all tests (unit + integration). Requires a running postgres.
# Set TEST_DATABASE_URL to override the default test database connection.
# -p 1 serializes package execution to avoid races on the shared db.Pool global.
test:
	go test -race -count=1 -p 1 ./...

# Run only store-layer integration tests with verbose output.
test-store:
	go test -race -count=1 -v ./internal/store/...

# Run only handler integration tests with verbose output.
test-handler:
	go test -race -count=1 -v ./internal/handler/...

# Generate a coverage report and open it as HTML.
test-coverage:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

lint:
	golangci-lint run

format:
	gofmt -w .

vet:
	go vet ./...

check: lint vet test

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

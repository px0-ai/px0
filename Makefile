.PHONY: install dev run build test lint format vet check docker-up docker-down

install:
	go mod download
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

dev:
	go run ./cmd/server

run:
	go run ./cmd/server

build:
	go build -o bin/server ./cmd/server

test:
	go test ./...

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

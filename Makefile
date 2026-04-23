.PHONY: up down test test-unit test-integration shell logs run-test build serve

# Start FRR container in the background
up:
	docker compose up -d frr

# Stop all containers and remove the socket volume
down:
	docker compose down -v

# Unit tests: pure Go, no FRR required, runs natively on macOS
test-unit:
	go test ./... -run 'Unit|unit' -v

# Integration tests: run inside Docker against a live mgmtd socket
test-integration: up
	docker compose run --rm test go test -v -tags integration -timeout 30s ./...

# Run both
test: test-unit test-integration

# Interactive shell in the test container
shell: up
	docker compose run --rm test bash

# Tail FRR daemon output
logs:
	docker compose logs -f frr

# Run a single test by name: make run-test T=TestIntegrationSessionCreate
run-test: up
	docker compose run --rm test go test -v -tags integration -run $(T) ./...

# Start the MCP HTTP/SSE server on http://localhost:3000
serve: up
	docker compose up -d server
	@echo "MCP server: http://localhost:3000/sse"

# Build the binary locally (requires CGO_ENABLED=0 for a static binary)
build:
	CGO_ENABLED=0 go build -o routing-mcp ./cmd/routing-mcp

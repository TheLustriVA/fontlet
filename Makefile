.PHONY: build run lint test clean

# Build the application
build:
	go build -o fontlet fontlet.go

# Run the application
run:
	go run fontlet.go

# Run linting checks (same as GitHub CI)
lint:
	~/go/bin/golangci-lint run --timeout=5m

# Run tests
test:
	go test -race -v ./...

# Clean build artifacts
clean:
	rm -f fontlet fontlet_*

# Install dependencies and tools
setup:
	go mod tidy
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# Run all checks before pushing
check: lint test build
	@echo "✅ All checks passed! Ready to push."
_:
    @just help

# List available commands
help:
    @just --list

# Run all tests
test:
    go test ./...
    go test -C bridge/cobra ./...
    go test -C bridge/urfave ./...

# Run tests and display coverage
coverage:
    #!/usr/bin/env sh
    set -e
    coverage_file=$(mktemp)
    trap 'rm -f "$coverage_file"' EXIT
    go test -coverprofile="$coverage_file" ./...
    go tool cover -func="$coverage_file"
    go test -C bridge/cobra -cover ./...
    go test -C bridge/urfave -cover ./...

# Update Go module files
tidy:
    go mod tidy
    go mod tidy -C bridge/urfave
    go mod tidy -C bridge/cobra
    go mod tidy -C examples/cli

# Check Go module files without modifying them
mod-check:
    go mod tidy -diff
    go mod tidy -C bridge/urfave -diff
    go mod tidy -C bridge/cobra -diff
    go mod tidy -C examples/cli -diff

# Format Go source files
format:
    golangci-lint fmt
    cd bridge/cobra && golangci-lint fmt
    cd bridge/urfave && golangci-lint fmt
    cd examples/cli && golangci-lint fmt

# Apply automatic fixes and format code
fix:
    golangci-lint run --fix
    golangci-lint fmt
    cd bridge/cobra && golangci-lint run --fix && golangci-lint fmt
    cd bridge/urfave && golangci-lint run --fix && golangci-lint fmt
    cd examples/cli && golangci-lint run --fix && golangci-lint fmt

# Run golangci-lint without --fix
lint:
    golangci-lint run
    cd bridge/cobra && golangci-lint run
    cd bridge/urfave && golangci-lint run
    cd examples/cli && golangci-lint run

# Check formatting without rewriting files
fmt-check:
    golangci-lint fmt --diff
    cd bridge/cobra && golangci-lint fmt --diff
    cd bridge/urfave && golangci-lint fmt --diff
    cd examples/cli && golangci-lint fmt --diff

# Verify all packages, bridges, and the example compile
build:
    go build ./...
    go build -C bridge/cobra ./...
    go build -C bridge/urfave ./...
    # -o /dev/null compiles the example without dropping a binary in the tree
    cd examples/cli && go build -o /dev/null ./...

# Run all non-mutating quality checks
check: mod-check test lint build fmt-check

# Remove build and coverage artifacts
clean:
    rm -rf coverage.out

alias cov := coverage
alias fmt := format

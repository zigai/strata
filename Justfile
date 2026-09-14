_:
    @just help

# List available commands
help:
    @just --list

# Run all tests
test:
    go test ./...

# Run tests and display coverage
coverage:
    #!/usr/bin/env sh
    set -e
    coverage_file=$(mktemp)
    trap 'rm -f "$coverage_file"' EXIT
    go test -coverprofile="$coverage_file" ./...
    go tool cover -func="$coverage_file"

# Update Go module files
tidy:
    go mod tidy

# Check Go module files without modifying them
mod-check:
    go mod tidy -diff

# Format Go source files
format:
    golangci-lint fmt

# Apply automatic fixes and format code
fix:
    golangci-lint run --fix
    golangci-lint fmt

# Run golangci-lint without --fix
lint:
    golangci-lint run

# Run all non-mutating quality checks
check: mod-check test lint
    golangci-lint fmt --diff

# Build the binary
build:
    go build -o strata .

# Install the binary
install:
    go install .

# Remove build artifacts
clean:
    rm -rf strata strata.exe dist/

# Build with version info (local dev)
build-dev:
    #!/usr/bin/env sh
    set -e
    commit=$(git rev-parse --short HEAD 2>/dev/null || echo none)
    build_date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    go build -ldflags "-X github.com/zigai/strata/internal/cli.version=dev -X github.com/zigai/strata/internal/cli.commit=${commit} -X github.com/zigai/strata/internal/cli.date=${build_date}" -o strata .


# Test goreleaser locally
release-dry-run:
    goreleaser release --snapshot --clean

# Pre-release safety checks
_release-check:
    #!/usr/bin/env sh
    set -e
    if [ -n "$(git status --porcelain)" ]; then
        echo "Error: uncommitted changes. Commit or stash first." >&2
        exit 1
    fi
    default_branch=$(git ls-remote --symref origin HEAD | awk '$1 == "ref:" { sub("refs/heads/", "", $2); print $2; exit }')
    if [ -z "$default_branch" ]; then
        echo "Error: could not determine the default branch for origin." >&2
        exit 1
    fi
    branch=$(git branch --show-current)
    if [ "$branch" != "$default_branch" ]; then
        echo "Error: not on $default_branch branch (on $branch)" >&2
        exit 1
    fi
    git fetch origin "$default_branch" --tags
    local_head=$(git rev-parse HEAD)
    remote_head=$(git rev-parse "origin/$default_branch")
    if [ "$local_head" != "$remote_head" ]; then
        echo "Error: local $default_branch differs from origin/$default_branch. Pull or push first." >&2
        exit 1
    fi
    latest_tag=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    if [ -n "$latest_tag" ]; then
        tag_commit=$(git rev-parse "$latest_tag"^{})
        if [ "$local_head" = "$tag_commit" ]; then
            echo "Error: HEAD is already tagged as $latest_tag. Make new commits first." >&2
            exit 1
        fi
    fi

# Release a new patch version (eg. v1.0.0 -> v1.0.1)
release-patch: _release-check
    #!/usr/bin/env sh
    set -e
    latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
    minor=$(echo "$latest" | sed 's/v//' | cut -d. -f2)
    patch=$(echo "$latest" | sed 's/v//' | cut -d. -f3)
    new="v${major}.${minor}.$((patch + 1))"
    echo "Releasing $new (was $latest)"
    git tag "$new"
    git push origin "$new"

# Release a new minor version (eg. v1.0.0 -> v1.1.0)
release-minor: _release-check
    #!/usr/bin/env sh
    set -e
    latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
    minor=$(echo "$latest" | sed 's/v//' | cut -d. -f2)
    new="v${major}.$((minor + 1)).0"
    echo "Releasing $new (was $latest)"
    git tag "$new"
    git push origin "$new"

# Release a new major version (eg. v1.0.0 -> v2.0.0)
release-major: _release-check
    #!/usr/bin/env sh
    set -e
    latest=$(git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
    major=$(echo "$latest" | sed 's/v//' | cut -d. -f1)
    new="v$((major + 1)).0.0"
    echo "Releasing $new (was $latest)"
    git tag "$new"
    git push origin "$new"

alias release := release-patch

alias cov := coverage
alias fmt := format

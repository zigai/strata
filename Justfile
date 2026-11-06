# Run unit tests in every Go module
[positional-arguments]
test *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if (( $# )); then
        go test "$@"
    else
        status=0
        for module in . bridge/cobra bridge/urfave examples/cli; do go -C "$module" test ./... || status=1; done
        exit "$status"
    fi

# Run Go race tests in every module
race:
    #!/usr/bin/env bash
    set -euo pipefail
    status=0
    for module in . bridge/cobra bridge/urfave examples/cli; do go -C "$module" test -race ./... || status=1; done
    exit "$status"

# Explore the config editors with the fuzzer, each target for the given time
fuzz time="60s":
    go test -run '^$' -fuzz '^FuzzUpdateTOML$' -fuzztime {{ time }} ./internal/edit/
    go test -run '^$' -fuzz '^FuzzUpdateYAML$' -fuzztime {{ time }} ./internal/edit/

# Measure all local test suites and print coverage
coverage:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf coverage
    mkdir -p coverage
    coverage_package=$(awk '$1 == "module" {gsub(/"/, "", $2); print $2; exit}' go.mod)
    module_coverage() (
        cd "$1"
        rm -rf coverage
        mkdir -p coverage
        coverage_package=$(awk '$1 == "module" {gsub(/"/, "", $2); print $2; exit}' go.mod)
        go test -coverpkg=./... ./... -args -test.gocoverdir="$PWD/coverage"
        go tool covdata percent -i coverage -pkg "$coverage_package/..."
        go tool covdata textfmt -i coverage -o coverage/profile.txt -pkg "$coverage_package/..."
        go tool cover -func coverage/profile.txt | awk '$1 == "total:"'
    )
    for module in . bridge/cobra bridge/urfave examples/cli; do module_coverage "$module"; done

# Format every supported first-party file type
format:
    @golangci-lint run --fix --enable-only whitespace --issues-exit-code=0
    @cd bridge/cobra && golangci-lint run --fix --enable-only whitespace --issues-exit-code=0
    @cd bridge/urfave && golangci-lint run --fix --enable-only whitespace --issues-exit-code=0
    @cd examples/cli && golangci-lint run --fix --enable-only whitespace --issues-exit-code=0
    @golangci-lint fmt
    @cd bridge/cobra && golangci-lint fmt
    @cd bridge/urfave && golangci-lint fmt
    @cd examples/cli && golangci-lint fmt
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r shfmt -f | xargs -r -d '\n' shfmt -w
    @git ls-files -co --exclude-standard -- Justfile justfile "*.just" | while IFS= read -r file; do [[ ! -f "$file" ]] || just --justfile "$file" --fmt || exit $?; done
    @just markdown fmt

# Apply safe lint autofixes, then format every supported file type
fix: && format
    golangci-lint run --fix
    cd bridge/cobra && golangci-lint run --fix
    cd bridge/urfave && golangci-lint run --fix
    cd examples/cli && golangci-lint run --fix

# Run every configured linter on the whole project, stopping on failure
lint:
    @golangci-lint run
    @cd bridge/cobra && golangci-lint run
    @cd bridge/urfave && golangci-lint run
    @cd examples/cli && golangci-lint run

# Check the same files and tools without writing
formatcheck:
    @golangci-lint run --enable-only whitespace
    @cd bridge/cobra && golangci-lint run --enable-only whitespace
    @cd bridge/urfave && golangci-lint run --enable-only whitespace
    @cd examples/cli && golangci-lint run --enable-only whitespace
    @golangci-lint fmt --diff
    @cd bridge/cobra && golangci-lint fmt --diff
    @cd bridge/urfave && golangci-lint fmt --diff
    @cd examples/cli && golangci-lint fmt --diff
    @git ls-files -co --exclude-standard -z | while IFS= read -r -d '' file; do if [[ -f $file ]]; then printf '%s\0' "$file"; fi; done | xargs -0 -r shfmt -f | xargs -r -d '\n' shfmt -d
    @git ls-files -co --exclude-standard -- Justfile justfile "*.just" | while IFS= read -r file; do [[ ! -f "$file" ]] || just --justfile "$file" --fmt --check || exit $?; done
    @just markdown check

# Verify all packages, bridges, and the example compile
build:
    go build ./...
    go build -C bridge/cobra ./...
    go build -C bridge/urfave ./...
    # -o /dev/null compiles the example without dropping a binary in the tree
    cd examples/cli && go build -o /dev/null ./...

# Run the commit checks
check: (run-checks "formatcheck" "lockcheck" "lint" "build" "test")

# Remove generated output and caches
clean: (remove "dist" "coverage.out" "*.test" "*.prof" "bridge/cobra/*.test" "bridge/cobra/*.prof" "bridge/cobra/coverage.out" "bridge/urfave/*.test" "bridge/urfave/*.prof" "bridge/urfave/coverage.out" "examples/cli/*.test" "examples/cli/*.prof" "examples/cli/coverage.out" "coverage" "bridge/cobra/coverage" "bridge/urfave/coverage" "examples/cli/coverage")

# Run the commit checks and slow suites
checkfull: (run-checks "formatcheck" "lockcheck" "lint" "build" "test" "race" "fuzz")

# Synchronize dependency locks without upgrading dependencies
[no-exit-message]
lock:
    @go mod tidy
    @cd "bridge/cobra" && go mod tidy
    @cd "bridge/urfave" && go mod tidy
    @cd "examples/cli" && go mod tidy

# Verify dependency manifests and locks without changing files
[no-exit-message]
lockcheck:
    @go mod tidy -diff
    @cd "bridge/cobra" && go mod tidy -diff
    @cd "bridge/urfave" && go mod tidy -diff
    @cd "examples/cli" && go mod tidy -diff

# Install repository dependencies
setup:
    @go mod download
    @cd "bridge/cobra" && go mod download
    @cd "bridge/urfave" && go mod download
    @cd "examples/cli" && go mod download

# List available recipes
[default]
help:
    @just --list

alias cov := coverage
alias fmt := format

[positional-arguments]
[private]
remove +paths:
    #!/usr/bin/env bash
    set -euo pipefail
    shopt -s nullglob globstar
    size() { LC_ALL=C numfmt --from-unit=1024 --to=iec-i --suffix=B --format=%.1f "$1"; }
    rows=() total=0
    for pattern in "$@"; do
        found=()
        for path in $pattern; do
            if [[ -e $path || -L $path ]]; then found+=("$path"); fi
        done
        if (( ${#found[@]} == 0 )); then continue; fi
        kib=$(du -sck -- "${found[@]}" | tail -n1 | cut -f1)
        rm -rf -- "${found[@]}"
        label=$pattern
        if (( ${#found[@]} > 1 )); then label+=" (${#found[@]})"; fi
        rows+=("removed  $label|$(size "$kib")")
        total=$(( total + kib ))
    done
    if [[ -n ${CLEAN_TOTAL_FILE:-} ]]; then echo "$total" >> "$CLEAN_TOTAL_FILE"; fi
    if (( ${#rows[@]} == 0 )); then echo "nothing to clean"; exit 0; fi
    printf '%s\n' "${rows[@]}" "total freed|$(size "$total")" | column -t -s '|' -R 2

[positional-arguments]
[private]
run-checks +steps:
    #!/usr/bin/env bash
    set -uo pipefail
    LC_NUMERIC=C
    log_dir="${XDG_CACHE_HOME:-$HOME/.cache}/just-check"
    mkdir -p "$log_dir"
    log=$(mktemp "$log_dir/step.XXXXXX")
    trap 'rm -f "$log"' EXIT
    width=0 failed=()
    for step in "$@"; do (( ${#step} > width )) && width=${#step}; done
    for step in "$@"; do
        read -ra command <<< "$step"
        printf 'check  %-*s  ' "$width" "$step"
        start=$EPOCHREALTIME
        run=(just --justfile {{ quote(justfile()) }} "${command[@]}")
        if [[ -t 1 ]]; then run=(script -qefc "${run[*]@Q}" /dev/null); fi
        if "${run[@]}" < /dev/null > "$log" 2>&1; then status=ok; else status=FAIL; failed+=("$step"); fi
        printf '%-4s  %7.1fs\n' "$status" "$(LC_NUMERIC=C awk -v a="$start" -v b="$EPOCHREALTIME" 'BEGIN { print b - a }')"
        if [[ "$status" == FAIL ]]; then sed 's/\r$//; s/^/    /' "$log"; fi
    done
    if (( ${#failed[@]} )); then printf '%d of %d failed: %s\n' "${#failed[@]}" "$#" "${failed[*]}"; exit 1; fi
    printf 'all %d passed\n' "$#"

[private]
markdown *args:
    rumdl {{ args }} --disable MD001,MD013,MD024,MD025,MD026,MD031,MD032,MD033,MD034,MD036,MD041,MD057,MD058 --config 'MD060.enabled=true' --config 'MD060.style="aligned"' --config 'MD003.style="atx"' --config 'MD004.style="dash"' --config 'MD029.style="ordered"' --config 'MD046.style="fenced"' --config 'MD048.style="backtick"' --config 'MD049.style="asterisk"' --config 'MD050.style="asterisk"' --config 'MD055.style="leading_and_trailing"'

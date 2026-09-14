# strata
[![Tests](https://github.com/zigai/strata/actions/workflows/test.yml/badge.svg)](https://github.com/zigai/strata/actions/workflows/test.yml)
[![Lint](https://github.com/zigai/strata/actions/workflows/lint.yml/badge.svg)](https://github.com/zigai/strata/actions/workflows/lint.yml)
[![Release](https://img.shields.io/github/v/release/zigai/strata?sort=semver)](https://github.com/zigai/strata/releases)
![Go version](https://img.shields.io/badge/go-1.27.0+-00ADD8.svg)

Layered, multi-format configuration library for Go with XDG cascading, provenance tracking, and CLI bridges

## Installation

```sh
go install github.com/zigai/strata@latest
```

### From Source

```sh
git clone https://github.com/zigai/strata.git
cd strata
go install .
```

### Releases

Download prebuilt binaries and packages from [GitHub Releases](https://github.com/zigai/strata/releases).

## Usage

```sh
strata --help
```

```sh
strata --version
```

## Development

### Requirements

* Go 1.27.0
* [just](https://github.com/casey/just)
* [golangci-lint](https://golangci-lint.run/)
* [GoReleaser](https://goreleaser.com/)

### Commands

```sh
just check
just test
just coverage
just lint
just build
```

```sh
just mod-check
just tidy
just format
just fix
just clean
```

### Release Dry Run

```sh
just release-dry-run
```

Release helpers are available through `just release-patch`, `just release-minor`, and `just release-major`.

## Project Layout

```text
.
├── main.go
├── api/
├── assets/
├── build/
├── cmd/
├── configs/
├── deployments/
├── docs/
├── examples/
├── init/
├── internal/
│   └── cli/
│       ├── root.go
│       └── root_test.go
├── pkg/
├── scripts/
├── test/
├── tools/
├── web/
├── go.mod
├── go.sum
├── Justfile
├── .goreleaser.yaml
├── .golangci.yaml
├── .github/
│   └── workflows/
│       ├── test.yml
│       └── lint.yml
├── .gitignore
└── README.md
```

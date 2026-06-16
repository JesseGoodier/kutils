# kutils — task runner. Run `just` to list recipes.

version := `cat VERSION | tr -d '[:space:]'`
ldflags := "-s -w -X main.version=" + version
tools := "kgc kgpv"

# Show available recipes
default:
    @just --list

# Install dev tooling (golangci-lint, govulncheck)
setup:
    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
    go install golang.org/x/vuln/cmd/govulncheck@latest
    go mod download

# Build both binaries for the host platform into ./bin
build:
    mkdir -p bin
    for tool in {{tools}}; do \
        go build -ldflags "{{ldflags}}" -o "bin/$tool" "./cmd/$tool"; \
    done

# Install both binaries into GOBIN
install:
    for tool in {{tools}}; do \
        go install -ldflags "{{ldflags}}" "./cmd/$tool"; \
    done

# Run tests
test:
    go test ./...

# Run go vet
vet:
    go vet ./...

# Lint (requires `just setup`)
lint:
    golangci-lint run ./...

# Auto-fix lint issues and format code (requires `just setup`)
fix:
    golangci-lint run --fix ./...
    golangci-lint fmt ./...

# All-in-one: auto-fix lint + format, then test
fixup: fix test

# Format all Go code
fmt:
    go fmt ./...
    gofmt -s -w .

# Tidy module dependencies
tidy:
    go mod tidy

# Update the Go toolchain directive and all dependencies to latest, then tidy
update:
    go get go@latest toolchain@latest
    go get -u ./...
    go mod tidy

# Security scan (requires `just setup`)
audit:
    govulncheck ./...

# Run all checks: fmt, vet, lint, test, audit
check: fmt vet lint test audit

# Remove build artifacts
clean:
    rm -rf bin

# Cross-compile release binaries for all platforms into ./dist
release-build:
    #!/usr/bin/env bash
    set -euo pipefail
    rm -rf dist && mkdir -p dist
    platforms="linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64"
    for platform in $platforms; do
        GOOS=${platform%/*}
        GOARCH=${platform#*/}
        for tool in {{tools}}; do
            ext=""
            [ "$GOOS" = "windows" ] && ext=".exe"
            out="dist/${tool}_{{version}}_${GOOS}_${GOARCH}${ext}"
            echo "building $out"
            CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
                go build -ldflags "{{ldflags}}" -o "$out" "./cmd/$tool"
        done
    done

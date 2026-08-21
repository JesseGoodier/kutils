# kutils dependency and CVE tasks
# Run `just` to see available recipes

set shell := ["bash", "-eu", "-o", "pipefail", "-c"]

# So `go install`ed tools (govulncheck) are on PATH
export PATH := `go env GOPATH` + "/bin:" + env("PATH")

default:
    @just --list

# Update all module dependencies to latest compatible versions
update-deps:
    go get -u ./...
    go mod tidy
    @git --no-pager diff --stat -- go.mod go.sum

# Update module dependencies to latest patch versions only
update-deps-patch:
    go get -u=patch ./...
    go mod tidy
    @git --no-pager diff --stat -- go.mod go.sum

# Tidy go.mod and go.sum
tidy:
    go mod tidy

# Scan for known Go vulnerabilities (same check as the release workflow)
cve: _install-govulncheck
    govulncheck ./...

alias vuln := cve
alias audit := cve

# Bump vulnerable modules to patched versions, tidy, and re-scan
fix-cves: _install-govulncheck
    #!/usr/bin/env bash
    set -euo pipefail

    echo "==> Scanning for known vulnerabilities..."
    set +e
    json=$(govulncheck -json ./... 2>/dev/null)
    status=$?
    set -e

    if [[ "$status" -eq 0 ]]; then
        echo "No vulnerabilities found."
        exit 0
    fi
    if [[ "$status" -ne 3 ]]; then
        echo "govulncheck failed (exit ${status})"
        exit "$status"
    fi

    mapfile -t updates < <(printf '%s\n' "$json" | jq -r '
        select(.finding != null)
        | select(.finding.fixed_version != null and .finding.fixed_version != "")
        | select(.finding.trace != null and (.finding.trace | length) > 0)
        | .finding.trace[0] as $root
        | select($root.module != null and $root.module != "" and $root.module != "stdlib")
        | "\($root.module)@\(.finding.fixed_version)"
    ' | sort -u)

    if [[ ${#updates[@]} -eq 0 ]]; then
        echo "Vulnerabilities found, but none can be auto-fixed (stdlib/toolchain, or no patched version)."
        echo "Show details with: just cve"
        echo "Try a broader bump with: just update-deps"
        govulncheck ./...
        exit 1
    fi

    echo "==> Bumping vulnerable modules:"
    printf '  %s\n' "${updates[@]}"
    go get "${updates[@]}"
    go mod tidy
    git --no-pager diff --stat -- go.mod go.sum

    echo "==> Re-scanning..."
    govulncheck ./...

# Install govulncheck if it is not already on PATH
[private]
_install-govulncheck:
    @command -v govulncheck >/dev/null || go install golang.org/x/vuln/cmd/govulncheck@latest

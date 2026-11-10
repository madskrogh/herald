#!/usr/bin/env bash
# PostToolUse hook: run goimports on the Go file Claude just wrote or edited.
# Non-blocking: if the file doesn't parse yet, leave it; `make ci` lints later.
file=$(jq -r '.tool_input.file_path // empty')
[[ "$file" == *.go && -f "$file" ]] || exit 0

PATH="$PATH:$(go env GOPATH 2>/dev/null)/bin"
if ! command -v goimports >/dev/null; then
  echo "goimports not found: go install golang.org/x/tools/cmd/goimports@latest" >&2
  exit 0
fi
goimports -w "$file" 2>/dev/null || true

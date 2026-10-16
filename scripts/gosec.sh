#!/usr/bin/env bash
set -euo pipefail

export GOWORK=off

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tool_dir="$(mktemp -d)"
trap 'rm -rf "$tool_dir"' EXIT

# Isolate scanner dependencies while using an importer compatible with our Go toolchain.
go -C "$repo_root/tools" build -o "$tool_dir/gosec" github.com/securego/gosec/v2/cmd/gosec
cd "$repo_root"
"$tool_dir/gosec" "$@"

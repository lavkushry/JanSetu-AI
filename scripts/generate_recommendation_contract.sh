#!/usr/bin/env bash
set -euo pipefail
# Go contract generation pins protoc and plugins. Rust uses vendored protoc.
if [[ "$(protoc --version)" != 'libprotoc 3.21.12' ]]; then
  echo 'Go protobuf generation requires protoc 3.21.12 (Ubuntu 24.04 protobuf-compiler).' >&2
  exit 1
fi
repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tool_dir="$(mktemp -d)"
trap 'rm -rf "$tool_dir"' EXIT
GOBIN="$tool_dir" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
GOBIN="$tool_dir" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.2
cd "$repo_dir"
PATH="$tool_dir:$PATH" protoc \
  --go_out=services/backend --go_opt=module=github.com/lavkushry/JanSetu-AI/services/backend \
  --go-grpc_out=services/backend --go-grpc_opt=module=github.com/lavkushry/JanSetu-AI/services/backend \
  contracts/proto/recommendation/v1/recommendation.proto

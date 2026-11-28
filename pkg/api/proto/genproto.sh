#!/usr/bin/env bash
# Regenerate the Supernova gRPC client/server from supernova.proto.
#
# Requires protoc, protoc-gen-go (v1.28.x) and protoc-gen-go-grpc (v1.2.x):
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28.1
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2.0
#
# The generated files land in ../ (supernova/pkg/api) as supernova.pb.go and
# supernova_grpc.pb.go and are committed.
set -euo pipefail
cd "$(dirname "$0")"

protoc --proto_path=. \
  --go_out=.. --go_opt=paths=source_relative \
  --go-grpc_out=.. --go-grpc_opt=paths=source_relative \
  supernova.proto

echo "generated supernova/pkg/api/supernova.pb.go and supernova_grpc.pb.go"

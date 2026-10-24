#!/bin/sh
set -eu

if ! command -v protoc >/dev/null 2>&1; then
	echo "protoc is required to generate protobuf bindings" >&2
	exit 1
fi

protobuf_include=""
if [ -f /usr/include/google/protobuf/empty.proto ]; then
	protobuf_include=/usr/include
elif command -v pkg-config >/dev/null 2>&1; then
	protobuf_include="$(pkg-config --variable=includedir protobuf 2>/dev/null || true)"
elif command -v brew >/dev/null 2>&1; then
	protobuf_include="$(brew --prefix protobuf)/include"
fi
if [ -z "$protobuf_include" ] || [ ! -f "$protobuf_include/google/protobuf/empty.proto" ]; then
	echo "Protobuf well-known type definitions are required (for example, install libprotobuf-dev or protobuf via Homebrew)" >&2
	exit 1
fi

tools_dir="$(pwd)/.tools/bin"
mkdir -p "$tools_dir"
GOBIN="$tools_dir" go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
GOBIN="$tools_dir" go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1

PATH="$tools_dir:$PATH" protoc \
	-I api/proto \
	-I "$protobuf_include" \
	--go_out=. --go_opt=module=github.com/DROWNING2003/Isodo \
	--go-grpc_out=. --go-grpc_opt=module=github.com/DROWNING2003/Isodo \
	$(find api/proto -type f -name '*.proto' -print)

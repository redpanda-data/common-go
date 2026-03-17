# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`code-sandbox` is a secure, multi-language code execution sandbox for Go. It runs untrusted JavaScript (via QuickJS) and Python (via RustPython) inside a WebAssembly runtime (wazero), providing memory isolation, resource limits, and a JSON-based bridge between Go and the sandboxed language.

This module is part of the `redpanda-data/common-go` monorepo but has its own `go.mod` — it is a standalone Go module.

## Build & Development Commands

All commands run from within the `code-sandbox/` directory unless noted.

```bash
# Run all tests
go test -v -race ./...

# Run a single test
go test -v -race -run TestBasicEval ./...

# Run benchmarks
go test -bench=. -benchmem -run=^$ ./...

# Lint (from monorepo root, requires Task: https://taskfile.dev)
task lint:dir DIRECTORY=code-sandbox

# Format (from monorepo root)
task fmt:dir DIRECTORY=code-sandbox

# Full CI check (fmt + lint + test, from monorepo root)
task test:dir DIRECTORY=code-sandbox
```

## Architecture

Three-layer design:

1. **Go Host Layer** (`codesandbox.go`) — Public API: `Sandbox` and `Interpreter` interfaces, option functions (`WithMaxMemory`, `WithMaxRuntime`, `WithRealtime`)
2. **WASM Runtime Layer** (wazero) — Memory isolation, resource limits, WASI support, `host_call` import bridge
3. **Language Runtime Layer** — QuickJS (JavaScript) or RustPython (Python) compiled to WASM

**WASM ABI contract** (applies to all language runtimes):
- Exports: `runtime_init`, `runtime_bind_function`, `runtime_eval`, `runtime_free`, `runtime_malloc_memory`, `runtime_free_memory`
- Imports (env module): `host_call(function_id, json_payload_ptr, output_ptr_ptr) -> i32`

**Key design patterns:**
- `Interpreter` compiles WASM once; `Sandbox` instantiates cheaply from it. Create one interpreter, reuse for many sandboxes.
- Public types are interfaces (`Sandbox`, `Interpreter`); concrete implementations are unexported.
- WASM binaries are embedded via `//go:embed` as gzip'd `[]byte`.
- The `host_call` bridge dispatches to Go callbacks via context value (`context.WithValue`).

## Code Conventions

- **License header**: Every `.go` file must begin with the Apache 2.0 copyright header (year 2025, Redpanda Data, Inc.)
- **Import ordering** (enforced by gci): standard library, then third-party, then `github.com/redpanda-data/common-go/...`
- **Formatting**: goimports, gofumpt, gci (all enforced by `task fmt:dir`)
- **Linting**: golangci-lint v2 with config at monorepo root `.golangci.yaml`. `nolintlint` requires both explanation and specificity.
- **Testing**: External test package (`package codesandbox_test`), table-driven subtests, `require` from testify. All public methods take `context.Context` as first arg.
- **Resource cleanup**: Always `defer interp.Close(ctx)` and `defer sandbox.Close(ctx)`.

## Rebuilding WASM Binaries

Pre-compiled WASM binaries are checked in. Only rebuild if modifying the runtime wrappers:

```bash
# JavaScript (requires Zig)
cd javascript && ./build-quickjs.sh

# Python (requires Rust + wasm32-wasip1 target)
cd python && ./build-python.sh
```

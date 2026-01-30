// Copyright 2025 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package javascript provides a convenient wrapper for creating JavaScript interpreters
// with an embedded QuickJS WebAssembly runtime.
//
// This package embeds the compiled QuickJS WASM binary at build time, eliminating the
// need to distribute or load the WASM file separately. It provides a simple API to
// create JavaScript interpreters that can be used to run sandboxed JavaScript code.
//
// # Usage
//
// This is the recommended entry point for most users:
//
//	import "github.com/redpanda-data/common-go/code-sandbox/javascript"
//
//	// Create an interpreter with embedded QuickJS WASM
//	interp, err := javascript.NewInterpreter(ctx)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer interp.Close(ctx)
//
//	// Create sandboxes from the interpreter
//	sandbox, err := codesandbox.NewSandbox(ctx, interp)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer sandbox.Close(ctx)
//
//	// Execute JavaScript code
//	result, err := sandbox.Eval(ctx, `2 + 2`)
//
// The embedded WASM binary is compiled from QuickJS using the build script
// in this directory (build-quickjs.sh).
package javascript

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"io"

	codesandbox "github.com/redpanda-data/common-go/code-sandbox"
)

// quickjsWasmBinary contains the compiled QuickJS WebAssembly module.
// This binary is embedded at build time from the quickjs.wasm file in this directory.
// The WASM module includes:
//   - QuickJS JavaScript engine (https://bellard.org/quickjs/)
//   - C wrapper for Go↔JavaScript bridge (qjs_runtime.c)
//   - WASI stubs for minimal system interface support
//
// The binary is compiled using Zig and optimized for size with LTO and dead code elimination.
// See build-quickjs.sh for the complete build process.
//
//go:embed quickjs.wasm.gz
var compressedQuickjsWasmBinary []byte

// NewInterpreter creates a new JavaScript interpreter using the embedded QuickJS WASM binary.
//
// This is a convenience wrapper around codesandbox.NewInterpreter that uses the embedded
// WASM binary, eliminating the need to load the binary from disk or manage its distribution.
//
// The interpreter compiles the WASM module once and can be reused to create multiple
// isolated sandbox instances efficiently.
//
// Parameters:
//   - ctx: Context for cancellation
//
// Returns:
//   - codesandbox.Interpreter: A compiled interpreter ready to create sandboxes
//   - error: Any error that occurred during compilation
//
// Example:
//
//	interp, err := javascript.NewInterpreter(ctx)
//	if err != nil {
//	    return err
//	}
//	defer interp.Close(ctx)
//
//	// Create multiple sandboxes from the same interpreter
//	sandbox1, _ := codesandbox.NewSandbox(ctx, interp)
//	sandbox2, _ := codesandbox.NewSandbox(ctx, interp)
func NewInterpreter(ctx context.Context) (codesandbox.Interpreter, error) {
	wasmBin, err := func() ([]byte, error) {
		rdr, err := gzip.NewReader(bytes.NewReader(compressedQuickjsWasmBinary))
		if err != nil {
			return nil, err
		}
		defer rdr.Close()
		return io.ReadAll(rdr)
	}()
	if err != nil {
		return nil, err
	}
	return codesandbox.NewInterpreter(ctx, wasmBin)
}

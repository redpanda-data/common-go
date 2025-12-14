#!/bin/bash
set -e

# Build Python WASM module using Rust/Cargo
# This script builds the RustPython WASM interpreter

BUILD_DIR="target/wasm32-wasip1/release"

echo "Building Python WASM module..."
echo ""

# Build the WASM module with Cargo
echo "Compiling RustPython interpreter for WASM..."
cargo build --release --target wasm32-wasip1

echo ""
echo "Compressing WASM module..."
gzip -c "$BUILD_DIR/python_interpreter.wasm" > python.wasm.gz

echo ""
echo "Build complete!"
echo ""
echo "WASM module:"
echo "  - python_interpreter.wasm: $BUILD_DIR/python_interpreter.wasm"
echo "  - Size: $(du -h $BUILD_DIR/python_interpreter.wasm | cut -f1)"
echo "  - python.wasm.gz: python.wasm.gz"
echo "  - Compressed size: $(du -h python.wasm.gz | cut -f1)"
echo ""
echo "Exported functions:"
echo "  - runtime_init() -> PythonRuntime*"
echo "  - runtime_bind_function(runtime, name) -> int (function_id)"
echo "  - runtime_eval(runtime, script, output_ptr) -> int (length, negative=error)"
echo "  - runtime_free(runtime)"
echo "  - runtime_malloc_memory(runtime, size) -> ptr"
echo "  - runtime_free_memory(runtime, ptr)"
echo ""
echo "Required WASM import:"
echo "  - host_call(function_id: i32, json_payload: i32, output_ptr: i32) -> i32 (length, negative=error)"
echo ""
echo "Optimization settings:"
echo "  - LTO: enabled"
echo "  - Codegen units: 1"
echo "  - Opt level: 3"
echo "  - Strip: enabled"
echo "  - Panic: abort"
echo ""

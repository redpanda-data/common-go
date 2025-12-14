#!/bin/bash
set -e

# Build QuickJS WASM module using zig cc
# This script builds only the WASM version

QUICKJS_DIR="quickjs"
BUILD_DIR="build"

echo "Building QuickJS WASM module..."

# Create build directory
mkdir -p "$BUILD_DIR"
echo ""
echo "Building WASM version..."
WASM_DIR="$BUILD_DIR/wasm"
mkdir -p "$WASM_DIR"

WASM_CC="zig cc -target wasm32-wasi"
WASM_CFLAGS="-O2 -Wall -D_GNU_SOURCE -DCONFIG_VERSION=\"\\\"2024-01-13\\\"\" -fvisibility=default -Xclang -mllvm -Xclang -wasm-enable-sjlj -flto -ffunction-sections -fdata-sections -DNDEBUG"
WASM_LDFLAGS="-O2 -flto -Wl,--gc-sections -Wl,--strip-all"

# Create a stub header for malloc_usable_size and setjmp for WASM
cat > "$WASM_DIR/wasm_stubs.h" << 'EOF'
#ifndef WASM_STUBS_H
#define WASM_STUBS_H
#include <stddef.h>

// Stub for malloc_usable_size
size_t malloc_usable_size(void *ptr);

// Prevent real setjmp.h from being included and provide stubs
#define _SETJMP_H
#define __SETJMP_H
#define setjmp(env) 0
#define longjmp(env, val) ((void)0)
typedef int jmp_buf[1];

#endif
EOF

# Create a stub implementation for malloc_usable_size and pthread for WASM
cat > "$WASM_DIR/wasm_stubs.c" << 'EOF'
#include <stddef.h>

// Stub for malloc_usable_size which is not available in WASM
size_t malloc_usable_size(void *ptr) {
    return 0;
}

// Pthread stubs for single-threaded WASM
typedef struct { int dummy; } pthread_mutex_t;
typedef struct { int dummy; } pthread_cond_t;
struct timespec { long tv_sec; long tv_nsec; };

int pthread_mutex_lock(pthread_mutex_t *mutex) { return 0; }
int pthread_mutex_unlock(pthread_mutex_t *mutex) { return 0; }
int pthread_cond_init(pthread_cond_t *cond, void *attr) { return 0; }
int pthread_cond_wait(pthread_cond_t *cond, pthread_mutex_t *mutex) { return 0; }
int pthread_cond_timedwait(pthread_cond_t *cond, pthread_mutex_t *mutex, const struct timespec *abstime) { return 0; }
int pthread_cond_destroy(pthread_cond_t *cond) { return 0; }
int pthread_cond_signal(pthread_cond_t *cond) { return 0; }
EOF

# Compile WASM stubs
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/wasm_stubs.o" "$WASM_DIR/wasm_stubs.c"

# Update WASM_CFLAGS to include the stub header
WASM_CFLAGS="$WASM_CFLAGS -include $WASM_DIR/wasm_stubs.h"

# Compile QuickJS core for WASM (without quickjs-libc for minimal build)
echo "Compiling QuickJS core for WASM..."
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/quickjs.o" "$QUICKJS_DIR/quickjs.c"
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/cutils.o" "$QUICKJS_DIR/cutils.c"
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/dtoa.o" "$QUICKJS_DIR/dtoa.c"
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/libregexp.o" "$QUICKJS_DIR/libregexp.c"
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/libunicode.o" "$QUICKJS_DIR/libunicode.c"

# Compile runtime library for WASM
echo "Compiling runtime library for WASM..."
$WASM_CC $WASM_CFLAGS -c -o "$WASM_DIR/qjs_runtime.o" "qjs_runtime.c"

# Link all WASM objects into a single WASM module with LTO
echo "Linking WASM module (LTO enabled for performance)..."
$WASM_CC $WASM_LDFLAGS -o "$WASM_DIR/quickjs.wasm" \
    "$WASM_DIR/wasm_stubs.o" \
    "$WASM_DIR/qjs_runtime.o" \
    "$WASM_DIR/quickjs.o" \
    "$WASM_DIR/cutils.o" \
    "$WASM_DIR/dtoa.o" \
    "$WASM_DIR/libregexp.o" \
    "$WASM_DIR/libunicode.o" \
    -rdynamic \
    -mexec-model=reactor

echo ""
echo "Compressing WASM module..."
gzip -c "$WASM_DIR/quickjs.wasm" > quickjs.wasm.gz
echo ""
echo "Build complete!"
echo ""
echo "WASM module:"
echo "  - quickjs.wasm: $WASM_DIR/quickjs.wasm"
echo "  - Size: $(du -h $WASM_DIR/quickjs.wasm | cut -f1)"
echo "  - quickjs.wasm.gz: quickjs.wasm.gz"
echo "  - Compressed size: $(du -h quickjs.wasm.gz | cut -f1)"
echo ""
echo "Exported functions:"
echo "  - runtime_init() -> QJSRuntime*"
echo "  - runtime_bind_function(runtime, name) -> int (function_id)"
echo "  - runtime_eval(runtime, script, output_ptr) -> int (length, negative=error)"
echo "  - runtime_free(runtime)"
echo "  - runtime_malloc_memory(runtime, size) -> ptr"
echo "  - runtime_free_memory(runtime, ptr)"
echo ""
echo "Required WASM import:"
echo "  - host_call(function_id: i32, json_payload: i32, output_ptr: i32) -> i32 (length, negative=error)"

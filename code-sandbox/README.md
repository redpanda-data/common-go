# Code Sandbox

A secure, multi-language code execution environment for Go using WebAssembly.

## Overview

Code Sandbox provides a safe, isolated environment for running untrusted code within Go applications. Built on [wazero](https://wazero.io/) (a pure Go WebAssembly runtime), it supports multiple language runtimes compiled to WASM, offering strong isolation guarantees without requiring CGO or native dependencies.

- **JavaScript** - Via [QuickJS](https://bellard.org/quickjs/) (ES2020 support)
- **Python** - Via [RustPython](https://rustpython.github.io/) (Python 3.10+ compatible)

### Key Features

- **Multi-Language Support** - Run JavaScript and Python in isolated sandboxes
- **Memory Isolation** - Sandboxed code runs in WebAssembly linear memory, completely isolated from Go memory
- **Resource Limits** - Configurable memory and CPU time limits prevent resource exhaustion
- **Bidirectional Communication** - JSON-based data exchange between Go and sandboxed languages
- **Host Function Binding** - Expose Go functions to sandboxed code for controlled host interaction
- **Pure Go Implementation** - No CGO dependencies, runs anywhere Go runs
- **Deterministic Execution** - Optional frozen time for reproducible testing
- **High Performance** - Compile once, create many sandboxes efficiently

### Use Cases

- Execute user-provided plugins safely (JavaScript, Python, etc.)
- Implement server-side scripting with resource controls
- Run untrusted code in multi-tenant environments
- Create programmable data transformation pipelines
- Build language-based DSLs with Go backends
- Provide scriptable automation in applications

## Architecture

The implementation consists of three layers:

```
┌─────────────────────────────────────┐
│         Go Application              │
│  (Your code using codesandbox)      │
└──────────────┬──────────────────────┘
               │ Go API
┌──────────────▼──────────────────────┐
│      Code Sandbox Package           │
│  - Sandbox interface (Eval, Bind)   │
│  - Interpreter (WASM compilation)   │
│  - JSON serialization bridge        │
└──────────────┬──────────────────────┘
               │ WASM imports/exports
┌──────────────▼──────────────────────┐
│     wazero WASM Runtime             │
│  - Memory isolation & limits        │
│  - CPU time controls                │
│  - WASI support                     │
└──────────────┬──────────────────────┘
               │ Execute WASM
┌──────────────▼──────────────────────┐
│    Language Runtime (WASM)          │
│                                     │
│  JavaScript: QuickJS (ES2020)       │
│  Python: RustPython (3.10+)         │
└─────────────────────────────────────┘
```

### Data Flow

The following shows data flow using JavaScript as an example. The same pattern applies to other language runtimes.

**Go → Language Runtime (Eval)**
1. Go calls `Eval(ctx, "2 + 2")`
2. String is copied to WASM memory
3. Language runtime executes the code (e.g., QuickJS for JavaScript)
4. Result is serialized to JSON
5. JSON is returned to Go as `json.RawMessage`

**Language Runtime → Go (Host Call)**
1. Sandboxed code calls bound function: `fetchData({id: 123})`
2. Argument is serialized to JSON
3. Runtime wrapper calls `host_call()` (WASM import)
4. Go callback is invoked with JSON payload
5. Go returns JSON result
6. Result is deserialized to a native value in the runtime

## Installation

```bash
go get github.com/redpanda-data/common-go/code-sandbox
```

### Requirements

- **Go 1.24+** - Required for the Go code
- **Zig (optional)** - Only needed if rebuilding the WASM binaries (pre-compiled binaries included)

## Quick Start

### JavaScript Example

The following example demonstrates JavaScript execution. Similar patterns apply to Python and other languages.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"

    "github.com/redpanda-data/common-go/code-sandbox"
    "github.com/redpanda-data/common-go/code-sandbox/javascript"
)

func main() {
    ctx := context.Background()

    // Create an interpreter (compile WASM once)
    interp, err := javascript.NewInterpreter(ctx)
    if err != nil {
        log.Fatal(err)
    }
    defer interp.Close(ctx)

    // Create a sandbox with limits
    sandbox, err := codesandbox.NewSandbox(ctx, interp,
        codesandbox.WithMaxMemory(10*1024*1024),  // 10MB limit
        codesandbox.WithMaxRuntime(5*time.Second)) // 5 second timeout
    if err != nil {
        log.Fatal(err)
    }
    defer sandbox.Close(ctx)

    // Execute JavaScript code
    result, err := sandbox.Eval(ctx, `
        const fibonacci = (n) => {
            if (n <= 1) return n;
            return fibonacci(n - 1) + fibonacci(n - 2);
        };
        fibonacci(10)
    `)
    if err != nil {
        log.Fatal(err)
    }

    var num int
    json.Unmarshal(result, &num)
    fmt.Println("Result:", num) // Output: Result: 55
}
```

### Python Example

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "time"

    "github.com/redpanda-data/common-go/code-sandbox"
    "github.com/redpanda-data/common-go/code-sandbox/python"
)

func main() {
    ctx := context.Background()

    // Create an interpreter (compile WASM once)
    interp, err := python.NewInterpreter(ctx)
    if err != nil {
        log.Fatal(err)
    }
    defer interp.Close(ctx)

    // Create a sandbox with limits
    sandbox, err := codesandbox.NewSandbox(ctx, interp,
        codesandbox.WithMaxMemory(10*1024*1024),  // 10MB limit
        codesandbox.WithMaxRuntime(5*time.Second)) // 5 second timeout
    if err != nil {
        log.Fatal(err)
    }
    defer sandbox.Close(ctx)

    // Execute Python code
    result, err := sandbox.Eval(ctx, `
def fibonacci(n):
    if n <= 1:
        return n
    return fibonacci(n - 1) + fibonacci(n - 2)

fibonacci(10)
    `)
    if err != nil {
        log.Fatal(err)
    }

    var num int
    json.Unmarshal(result, &num)
    fmt.Println("Result:", num) // Output: Result: 55
}
```

## Usage Guide

The following usage guide uses JavaScript as the primary example. The same patterns apply to Python and other language runtimes.

### Creating Interpreters and Sandboxes

**Best Practice:** Create one `Interpreter` and reuse it to create multiple `Sandbox` instances.

```go
// JavaScript example
jsInterp, err := javascript.NewInterpreter(ctx)
if err != nil {
    return err
}
defer jsInterp.Close(ctx)

// Python example
pyInterp, err := python.NewInterpreter(ctx)
if err != nil {
    return err
}
defer pyInterp.Close(ctx)

// Create many sandboxes from each interpreter (cheap - uses compiled modules)
jsSandbox1, _ := codesandbox.NewSandbox(ctx, jsInterp)
jsSandbox2, _ := codesandbox.NewSandbox(ctx, jsInterp)

pySandbox1, _ := codesandbox.NewSandbox(ctx, pyInterp)
pySandbox2, _ := codesandbox.NewSandbox(ctx, pyInterp)
```

Each sandbox is completely isolated with its own language runtime state and global scope.

### Executing Code

Use `Eval()` to execute code and receive JSON results. Examples shown for both JavaScript and Python:

```go
// JavaScript examples
result, _ := jsSandbox.Eval(ctx, `2 + 2`)
// result: json.RawMessage containing 4

result, _ = jsSandbox.Eval(ctx, `({name: "Alice", age: 30})`)
// result: json.RawMessage containing {"name":"Alice","age":30}

result, _ = jsSandbox.Eval(ctx, `[1, 2, 3].map(x => x * 2)`)
// result: json.RawMessage containing [2,4,6]

result, _ = jsSandbox.Eval(ctx, `
    const data = [1, 2, 3, 4, 5];
    const sum = data.reduce((a, b) => a + b, 0);
    const avg = sum / data.length;
    avg
`)
// result: json.RawMessage containing 3

// Python examples
result, _ = pySandbox.Eval(ctx, `2 + 2`)
// result: json.RawMessage containing 4

result, _ = pySandbox.Eval(ctx, `{"name": "Alice", "age": 30}`)
// result: json.RawMessage containing {"name":"Alice","age":30}

result, _ = pySandbox.Eval(ctx, `[x * 2 for x in [1, 2, 3]]`)
// result: json.RawMessage containing [2,4,6]

result, _ = pySandbox.Eval(ctx, `
data = [1, 2, 3, 4, 5]
sum(data) / len(data)
`)
// result: json.RawMessage containing 3.0
```

The result of the last expression is automatically serialized to JSON and returned.

### Binding Go Functions

Expose Go functions to sandboxed code using `Bind()`. Examples for both JavaScript and Python:

```go
// Simple function
sandbox.Bind(ctx, "add", func(data json.RawMessage) (json.RawMessage, error) {
    var nums []int
    if err := json.Unmarshal(data, &nums); err != nil {
        return nil, err
    }
    result := nums[0] + nums[1]
    return json.Marshal(result)
})

// JavaScript: add([5, 7]) // Returns 12
// Python: add([5, 7])     // Returns 12

// Complex function with structs
type User struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

sandbox.Bind(ctx, "getUser", func(data json.RawMessage) (json.RawMessage, error) {
    var userID string
    json.Unmarshal(data, &userID)

    // Fetch from database, API, etc.
    user := User{ID: userID, Name: "Alice"}

    return json.Marshal(user)
})

// JavaScript: const user = getUser("user-123")
// Python: user = getUser("user-123")
// Both return: {"id": "user-123", "name": "Alice"}
```

### Configuration Options

#### Memory Limits

Restrict the maximum memory available to the sandbox:

```go
sandbox, err := codesandbox.NewSandbox(ctx, interp,
    codesandbox.WithMaxMemory(5 * 1024 * 1024)) // 5MB limit
```

Memory is specified in bytes and rounded down to 64KB pages (WASM page size).

**Default:** 4GB (65536 pages)

#### Execution Time Limits

Limit how long JavaScript can run:

```go
sandbox, err := codesandbox.NewSandbox(ctx, interp,
    codesandbox.WithMaxRuntime(3 * time.Second)) // 3 second timeout
```

When combined with context cancellation, this ensures runaway scripts are terminated.

**Default:** No time limit

#### Real-Time vs Deterministic Clock

By default, time is frozen (deterministic) for reproducible testing:

```go
// Deterministic time (default)
sandbox, _ := codesandbox.NewSandbox(ctx, interp)

// JavaScript: Date.now() always returns the same value
result, _ := sandbox.Eval(ctx, `Date.now()`)

// Python: time.time() always returns the same value
result, _ = sandbox.Eval(ctx, `import time; time.time()`)

// Real wall-clock time
sandbox, _ := codesandbox.NewSandbox(ctx, interp,
    codesandbox.WithRealtime())

// Both JavaScript Date.now() and Python time.time() return actual current time
```

#### Combining Options

All options can be combined:

```go
sandbox, err := codesandbox.NewSandbox(ctx, interp,
    codesandbox.WithMaxMemory(10 * 1024 * 1024),
    codesandbox.WithMaxRuntime(5 * time.Second),
    codesandbox.WithRealtime())
```

## Security and Isolation

### Isolation Guarantees

1. **Memory Isolation** - Sandboxed code cannot access Go heap memory. All data exchange goes through explicit JSON serialization.

2. **CPU Limits** - Context cancellation can terminate execution. Use `WithMaxRuntime()` to enforce time limits.

3. **Memory Limits** - `WithMaxMemory()` prevents the sandbox from exhausting system memory.

4. **No Direct I/O** - Sandboxed code has no access to:
   - Filesystem
   - Network sockets
   - Environment variables
   - Operating system APIs

   Access is only possible through explicitly bound Go functions.

5. **No Shared State** - Each sandbox instance is completely isolated from others.

### Best Practices for Secure Execution

```go
// 1. Always set resource limits
sandbox, err := codesandbox.NewSandbox(ctx, interp,
    codesandbox.WithMaxMemory(maxMemory),
    codesandbox.WithMaxRuntime(maxTime))

// 2. Use context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
result, err := sandbox.Eval(ctx, untrustedCode)

// 3. Validate and sanitize bound function inputs
sandbox.Bind(ctx, "query", func(data json.RawMessage) (json.RawMessage, error) {
    var query string
    if err := json.Unmarshal(data, &query); err != nil {
        return nil, err
    }

    // Validate input
    if !isValidQuery(query) {
        return nil, errors.New("invalid query")
    }

    // Execute safely
    result := executeQuery(query)
    return json.Marshal(result)
})

// 4. Handle sandboxed code errors gracefully
result, err := sandbox.Eval(ctx, userCode)
if err != nil {
    // Log error without exposing sensitive details
    log.Printf("Sandbox execution failed: %v", err)
    return sanitizedError
}
```

## API Reference

### Core Types

#### `Sandbox` Interface

```go
type Sandbox interface {
    Bind(ctx context.Context, name string, cb Callback) error
    Eval(ctx context.Context, script string) (json.RawMessage, error)
    Close(ctx context.Context) error
}
```

#### `Interpreter` Interface

```go
type Interpreter interface {
    Close(ctx context.Context) error
}
```

#### `Callback` Function Type

```go
type Callback func(json.RawMessage) (json.RawMessage, error)
```

### Functions

#### `javascript.NewInterpreter(ctx) (Interpreter, error)`

Creates a new JavaScript interpreter with embedded QuickJS WASM binary.

#### `python.NewInterpreter(ctx) (Interpreter, error)`

Creates a new Python interpreter with embedded RustPython WASM binary.

#### `codesandbox.NewSandbox(ctx, interp, opts...) (Sandbox, error)`

Creates a new isolated sandbox from an interpreter.

### Configuration Options

- `WithMaxMemory(bytes uint32)` - Set maximum memory limit
- `WithMaxRuntime(duration time.Duration)` - Set maximum execution time
- `WithRealtime()` - Enable real wall-clock time

## Building from Source

### Language Runtimes

Pre-compiled WASM binaries for JavaScript (QuickJS) and Python (RustPython) are embedded in their respective packages. To rebuild them:

### Prerequisites

```bash
# Install Zig (for cross-compilation to WASM)
# macOS
brew install zig

# Linux
# Download from https://ziglang.org/download/

# Windows
# Download from https://ziglang.org/download/
```

### Build Steps

**JavaScript (QuickJS):**
```bash
cd javascript
./build-quickjs.sh
```

This script:
1. Clones QuickJS from the git submodule
2. Compiles QuickJS and the C wrapper (`qjs_runtime.c`) to WASM
3. Links with WASI stubs
4. Optimizes with LTO and dead code elimination
5. Outputs `quickjs.wasm` (~1.5MB, gzipped to ~400KB)

The build uses Zig's WASM target with optimization level `-O2`, LTO, and dead code elimination.

**Python (RustPython):**
```bash
cd python
./build-python.sh
```

This script:
1. Compiles the Rust wrapper (`src/lib.rs`) linking RustPython
2. Uses Rust's wasm32-wasi target with full optimizations
3. Strips symbols and optimizes for size
4. Outputs `python.wasm` (~3MB, gzipped to ~800KB)

The build uses Cargo with `opt-level=3`, `lto=true`, and `codegen-units=1` for maximum optimization.

### Benchmarks

Typical performance characteristics:

- **Interpreter creation:** ~10-50ms (compile WASM once)
- **Sandbox creation:** ~1-5ms (instantiate modules)
- **Simple evaluation:** ~0.1-1ms
- **Host function call:** ~0.05-0.5ms

*Note: Actual performance depends on hardware, code complexity, and memory limits.*

## Limitations

### General
- **JSON-only communication** - All data exchange between Go and sandboxed code uses JSON serialization
- **Single-threaded** - Each sandbox runs in a single goroutine

### JavaScript-Specific (QuickJS)
- **JavaScript Version** - Supports ES2020 features (QuickJS limitation)
- **No async/await** - QuickJS doesn't support async JavaScript
- **Limited built-ins** - Only basic JavaScript globals available (no DOM, fetch, etc.)

### Python-Specific (RustPython)
- **Python Version** - Supports Python 3.10+ syntax (RustPython target)
- **Standard library** - Limited to core built-ins compiled into WASM
- **No C extensions** - Cannot load native Python extensions (NumPy, etc.)
- **Import restrictions** - Only modules compiled into the WASM binary are available

## Examples

### Example 1: Data Transformation Pipeline

```go
func transformData(input []byte) ([]byte, error) {
    ctx := context.Background()
    interp, _ := javascript.NewInterpreter(ctx)
    defer interp.Close(ctx)

    sandbox, _ := codesandbox.NewSandbox(ctx, interp)
    defer sandbox.Close(ctx)

    // Bind custom transformation functions
    sandbox.Bind(ctx, "fetchMetadata", func(data json.RawMessage) (json.RawMessage, error) {
        // Fetch from database or API
        return json.Marshal(map[string]string{"status": "active"})
    })

    // Execute transformation
    script := `
        const data = ` + string(input) + `;
        const metadata = fetchMetadata(data.id);
        {
            ...data,
            metadata,
            processed: true,
            timestamp: Date.now()
        }
    `

    return sandbox.Eval(ctx, script)
}
```

### Example 2: User Plugin System

```go
type Plugin struct {
    Name   string
    Script string
}

func executePlugin(plugin Plugin, input interface{}) (interface{}, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    interp, _ := javascript.NewInterpreter(ctx)
    defer interp.Close(ctx)

    sandbox, _ := codesandbox.NewSandbox(ctx, interp,
        codesandbox.WithMaxMemory(5*1024*1024),
        codesandbox.WithMaxRuntime(3*time.Second))
    defer sandbox.Close(ctx)

    // Bind safe API functions
    sandbox.Bind(ctx, "log", func(data json.RawMessage) (json.RawMessage, error) {
        log.Printf("[Plugin %s] %s", plugin.Name, string(data))
        return json.Marshal(nil)
    })

    inputJSON, _ := json.Marshal(input)
    script := fmt.Sprintf(`
        const input = %s;
        %s
    `, string(inputJSON), plugin.Script)

    result, err := sandbox.Eval(ctx, script)
    if err != nil {
        return nil, fmt.Errorf("plugin %s failed: %w", plugin.Name, err)
    }

    var output interface{}
    json.Unmarshal(result, &output)
    return output, nil
}
```

## License

Licensed under the Apache License, Version 2.0. See the LICENSE file for details.

## Contributing

This project is part of the Redpanda Data common-go repository. For contributions, please refer to the main repository guidelines.

## Credits

- **QuickJS** - Fabrice Bellard (https://bellard.org/quickjs/)
- **RustPython** - RustPython Team (https://rustpython.github.io/)
- **wazero** - Tetrate Labs (https://wazero.io/)

## Related Projects

### Multi-Language Sandboxes
- [Extism](https://extism.org/) - Universal plugin system using WebAssembly (multiple languages)
- [Wasmtime](https://wasmtime.dev/) - Standalone WASM runtime (not Go-native)

### JavaScript-Specific Runtimes
- [goja](https://github.com/dop251/goja) - Pure Go JavaScript engine (no WASM isolation)
- [v8go](https://github.com/rogchap/v8go) - V8 bindings for Go (requires CGO)
- [otto](https://github.com/robertkrimen/otto) - Pure Go JavaScript interpreter (ES5 only)

This project uniquely combines multi-language support, WASM isolation, and pure Go implementation without CGO dependencies.

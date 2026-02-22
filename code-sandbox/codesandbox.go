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

// Package codesandbox provides a secure code execution environment using WebAssembly.
//
// This package enables running untrusted code in isolated sandboxes powered by language
// runtimes compiled to WebAssembly. Currently supports JavaScript via QuickJS and Python
// via RustPython. The sandbox provides:
//
//   - Memory isolation through WebAssembly linear memory
//   - Configurable memory and CPU time limits
//   - Bidirectional Go ↔ language runtime communication via JSON
//   - Host function binding to expose Go functions to sandboxed code
//   - Deterministic or real-time execution modes
//
// # Architecture
//
// The implementation consists of three layers:
//
//  1. Go Host Layer - This package provides the Go API
//  2. WASM Runtime Layer - wazero executes the WASM module in a sandbox
//  3. Language Runtime - Language interpreter/VM compiled to WASM (QuickJS for JavaScript, RustPython for Python)
//
// Communication between Go and the sandboxed language uses JSON as the universal data format.
// Go functions can be bound to the runtime, and code can be evaluated with results returned to Go.
//
// # Basic Usage
//
// JavaScript Example:
//
//	// Create an interpreter (compile WASM once, reuse for multiple sandboxes)
//	interp, err := javascript.NewInterpreter(ctx)
//	if err != nil {
//	    return err
//	}
//	defer interp.Close(ctx)
//
//	// Create a sandbox with memory and time limits
//	sandbox, err := codesandbox.NewSandbox(ctx, interp,
//	    codesandbox.WithMaxMemory(10*1024*1024), // 10MB limit
//	    codesandbox.WithMaxRuntime(5*time.Second)) // 5 second timeout
//	if err != nil {
//	    return err
//	}
//	defer sandbox.Close(ctx)
//
//	// Bind a Go function to JavaScript
//	sandbox.Bind(ctx, "multiply", func(data json.RawMessage) (json.RawMessage, error) {
//	    var nums []int
//	    json.Unmarshal(data, &nums)
//	    result := nums[0] * nums[1]
//	    return json.Marshal(result)
//	})
//
//	// Execute JavaScript code
//	result, err := sandbox.Eval(ctx, `multiply([6, 7])`)
//	if err != nil {
//	    return err
//	}
//	fmt.Println(string(result)) // Output: 42
//
// Python Example:
//
//	// Create a Python interpreter
//	interp, err := python.NewInterpreter(ctx)
//	if err != nil {
//	    return err
//	}
//	defer interp.Close(ctx)
//
//	// Create a sandbox
//	sandbox, err := codesandbox.NewSandbox(ctx, interp)
//	if err != nil {
//	    return err
//	}
//	defer sandbox.Close(ctx)
//
//	// Bind a Go function to Python
//	sandbox.Bind(ctx, "multiply", func(data json.RawMessage) (json.RawMessage, error) {
//	    var nums []int
//	    json.Unmarshal(data, &nums)
//	    result := nums[0] * nums[1]
//	    return json.Marshal(result)
//	})
//
//	// Execute Python code
//	result, err := sandbox.Eval(ctx, `multiply([6, 7])`)
//	if err != nil {
//	    return err
//	}
//	fmt.Println(string(result)) // Output: 42
//
// # Security and Isolation
//
// The sandbox provides strong isolation guarantees:
//
//   - Sandboxed code cannot access Go memory directly
//   - Memory limits prevent resource exhaustion
//   - Context cancellation can terminate runaway scripts
//   - No filesystem or network access by default
//   - Pure Go implementation with no CGO dependencies
//
// # Performance
//
// For optimal performance, create one Interpreter and reuse it to create multiple
// Sandbox instances. The Interpreter compiles the WASM module once and caches it,
// while each Sandbox provides an isolated execution environment.
package codesandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"
	"unsafe"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

// Callback is a function signature for Go functions that can be called from sandboxed code.
// When a bound function is called from the sandboxed runtime, the argument is serialized
// to JSON and passed as a json.RawMessage. The callback must return a JSON-serializable
// result or an error.
//
// Example (JavaScript):
//
//	sandbox.Bind(ctx, "add", func(data json.RawMessage) (json.RawMessage, error) {
//	    var nums []int
//	    if err := json.Unmarshal(data, &nums); err != nil {
//	        return nil, err
//	    }
//	    result := nums[0] + nums[1]
//	    return json.Marshal(result)
//	})
//
// The sandboxed code can then call: add([1, 2]) // Returns 3
type Callback func(json.RawMessage) (json.RawMessage, error)

type implKeyType int

const implKey implKeyType = 0

// Sandbox represents an isolated code execution environment.
//
// A Sandbox instance provides methods to:
//   - Execute code via Eval
//   - Expose Go functions to sandboxed code via Bind
//   - Clean up resources via Close
//
// Each Sandbox instance maintains its own isolated state and cannot interfere with
// other sandboxes. Multiple sandboxes can be created from a single Interpreter for
// efficient resource usage.
//
// Memory and CPU limits are configured when creating the sandbox and apply to all
// operations within that sandbox.
//
// Important: Always call Close when done to release WASM runtime resources.
type Sandbox interface {
	// Bind exposes a Go function to the sandboxed code under the given name.
	// The function will be available as a global variable in the runtime.
	//
	// The callback receives JSON-encoded arguments from the sandboxed code and must
	// return JSON-encoded results. If the callback returns an error, the sandboxed
	// code will receive null/nil as the return value.
	//
	// Example (JavaScript):
	//   sandbox.Bind(ctx, "fetchUser", func(data json.RawMessage) (json.RawMessage, error) {
	//       var userID string
	//       json.Unmarshal(data, &userID)
	//       user := fetchUserFromDB(userID)
	//       return json.Marshal(user)
	//   })
	//
	// The sandboxed code can then call: const user = fetchUser("user-123")
	Bind(ctx context.Context, name string, cb Callback) error

	// Eval executes code in the sandbox and returns the result as JSON.
	//
	// The script is evaluated, and the result of the last expression is serialized
	// to JSON and returned. Runtime errors and exceptions are returned as Go errors.
	//
	// Example (JavaScript):
	//   result, err := sandbox.Eval(ctx, "2 + 2")
	//   // result is json.RawMessage containing: 4
	//
	//   result, err := sandbox.Eval(ctx, `({name: "Alice", age: 30})`)
	//   // result is json.RawMessage containing: {"name":"Alice","age":30}
	//
	// The context can be used to cancel execution if the script exceeds time limits.
	Eval(ctx context.Context, script string) (json.RawMessage, error)

	// Reset clears the sandbox state and reinitializes the runtime to a fresh state.
	//
	// This method provides a faster alternative to closing and creating a new sandbox when you
	// need to run multiple isolated code executions sequentially. Reset:
	//   - Closes the current module instance and reinitializes it
	//   - Clears all runtime state (variables, function definitions, etc.)
	//   - Preserves bound Go functions (callbacks remain available)
	//   - Reuses the compiled modules and runtime (faster than NewSandbox)
	//
	// Use Reset when:
	//   - Running multiple independent scripts that shouldn't share state
	//   - Implementing a sandbox pool for sequential reuse
	//   - You want to keep bound functions but clear runtime state
	//
	// Example:
	//   sandbox.Eval(ctx, "x = 42")  // Define variable
	//   sandbox.Reset(ctx)           // Clear state
	//   sandbox.Eval(ctx, "x")       // Error: x is undefined
	//
	// Note: Reset is significantly faster than Close + NewSandbox because it reuses
	// the compiled WASM modules and runtime instance.
	//
	// If this function returns an error, [Sandbox.Close] must be still be called.
	Reset(ctx context.Context) error

	// Close releases all resources associated with the sandbox.
	// After calling Close, no further operations can be performed on this sandbox.
	// It is safe to call Close multiple times.
	Close(ctx context.Context) error
}

// Interpreter represents a compiled WASM module that can be used to create sandboxes.
//
// An Interpreter performs the expensive one-time compilation of a language runtime
// WASM binary and caches the compiled module. Multiple Sandbox instances can be created
// from a single Interpreter, providing efficient resource usage when running many
// isolated execution environments.
//
// The Interpreter maintains:
//   - Compiled WASM modules (language runtime, WASI, and host functions)
//   - Runtime configuration (compilation cache, core features)
//   - Compilation cache for fast instantiation
//
// Important: Always call Close when done to release compiled module resources.
type Interpreter interface {
	runtimeConfig() wazero.RuntimeConfig
	module() wazero.CompiledModule
	wasip1Module() wazero.CompiledModule
	hostModule() wazero.CompiledModule

	// Close releases all resources associated with the interpreter, including
	// compiled modules and the compilation cache.
	Close(ctx context.Context) error
}

// boundCallback holds a callback function and its name for re-binding after Reset
type boundCallback struct {
	name     string
	callback Callback
}

// impl is the concrete implementation of the Sandbox interface.
// It manages the WASM runtime, modules, and the bridge between Go and the sandboxed language.
//
// Fields:
//   - rt: The wazero runtime instance that executes WASM code
//   - compiledMod: The compiled language runtime WASM module (for re-instantiation during Reset)
//   - currentMod: The current instantiated language runtime WASM module
//   - modConfig: The module configuration used for instantiation (preserved for Reset)
//   - handle: Opaque pointer to the runtime structure in WASM memory
//   - callbacks: Map of function IDs to bound callbacks (includes name for re-binding during Reset)
type impl struct {
	rt          wazero.Runtime
	compiledMod wazero.CompiledModule
	currentMod  api.Module
	modConfig   wazero.ModuleConfig
	handle      uint64
	callbacks   map[int32]boundCallback
}

// implCfg holds the configuration options for creating a sandbox.
//
// Fields:
//   - memoryPages: Maximum memory in 64KB pages (default: 65536 = 4GB)
//   - maxRuntime: Maximum execution time before context cancellation
//   - useRealtime: Whether to use real wall-clock time (default: false = deterministic)
type implCfg struct {
	memoryPages uint32
	maxRuntime  time.Duration
	useRealtime bool
}

// SandboxOpt is a function that modifies sandbox configuration.
// Options are applied when creating a new sandbox via NewSandbox.
type SandboxOpt func(*implCfg)

// WithMaxMemory sets the maximum memory the sandbox can use, in bytes.
// The value is rounded down to the nearest 64KB page (WASM page size).
//
// Default: 4GB (65536 pages)
//
// Example:
//
//	sandbox, err := codesandbox.NewSandbox(ctx, interp,
//	    codesandbox.WithMaxMemory(10 * 1024 * 1024)) // 10MB limit
func WithMaxMemory(maxBytes uint32) SandboxOpt {
	pages := maxBytes / 65536
	return func(ic *implCfg) { ic.memoryPages = min(ic.memoryPages, pages) }
}

// WithMaxRuntime sets the maximum execution time for the sandbox.
// This works in conjunction with context cancellation. When the context
// is cancelled after the specified duration, WASM execution will be terminated.
//
// Default: No time limit
//
// Example:
//
//	sandbox, err := codesandbox.NewSandbox(ctx, interp,
//	    codesandbox.WithMaxRuntime(5 * time.Second)) // 5 second timeout
func WithMaxRuntime(d time.Duration) SandboxOpt {
	return func(ic *implCfg) { ic.maxRuntime = d }
}

// WithRealtime enables real wall-clock time in the sandbox.
// By default, time is deterministic (frozen), which is useful for testing.
// When real-time is enabled, time-related functions in the sandboxed code
// will return actual wall-clock time:
//   - JavaScript: Date.now()
//   - Python: time.time()
//
// Default: Deterministic (frozen) time
//
// Example:
//
//	sandbox, err := codesandbox.NewSandbox(ctx, interp,
//	    codesandbox.WithRealtime()) // Enable real-time clock
func WithRealtime() SandboxOpt {
	return func(ic *implCfg) { ic.useRealtime = true }
}

var _ Sandbox = (*impl)(nil)

// NewSandbox creates a new isolated code sandbox from a compiled interpreter.
//
// The sandbox is configured with optional settings for memory limits, execution time,
// and time behavior. Each sandbox maintains independent state and can be used
// concurrently with other sandboxes created from the same interpreter.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - i: A compiled Interpreter (created via NewInterpreter or language-specific constructor)
//   - opts: Optional configuration functions (WithMaxMemory, WithMaxRuntime, WithRealtime)
//
// Returns:
//   - Sandbox: A new sandbox instance ready for code execution
//   - error: Any error that occurred during initialization
//
// Example:
//
//	// JavaScript
//	jsInterp, _ := javascript.NewInterpreter(ctx)
//	jsSandbox, err := codesandbox.NewSandbox(ctx, jsInterp,
//	    codesandbox.WithMaxMemory(10*1024*1024),
//	    codesandbox.WithMaxRuntime(5*time.Second))
//
//	// Python
//	pyInterp, _ := python.NewInterpreter(ctx)
//	pySandbox, err := codesandbox.NewSandbox(ctx, pyInterp,
//	    codesandbox.WithMaxMemory(10*1024*1024),
//	    codesandbox.WithMaxRuntime(5*time.Second))
//
// The sandbox must be closed when no longer needed to release WASM runtime resources.
func NewSandbox(ctx context.Context, i Interpreter, opts ...SandboxOpt) (Sandbox, error) {
	iCfg := &implCfg{
		memoryPages: 65536,
		maxRuntime:  time.Duration(math.MaxInt64),
	}
	for _, opt := range opts {
		opt(iCfg)
	}
	rtCfg := i.runtimeConfig().WithMemoryLimitPages(iCfg.memoryPages)
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	mCfg := wazero.NewModuleConfig()

	if iCfg.useRealtime {
		now := time.Now()
		mCfg = mCfg.WithWalltime(func() (sec int64, nsec int32) {
			return now.Unix(), int32(now.Nanosecond())
		}, sys.ClockResolution(time.Millisecond.Nanoseconds()))
	}
	if _, err := rt.InstantiateModule(ctx, i.hostModule(), mCfg); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	if _, err := rt.InstantiateModule(ctx, i.wasip1Module(), mCfg); err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	mod, err := rt.InstantiateModule(ctx, i.module(), mCfg)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	results, err := mod.ExportedFunction("runtime_init").Call(ctx)
	if err != nil {
		_ = rt.Close(ctx)
		return nil, err
	}
	return &impl{
		rt,
		i.module(),
		mod,
		mCfg,
		results[0],
		make(map[int32]boundCallback, 5),
	}, nil
}

// Bind implements Sandbox.
func (i *impl) Bind(ctx context.Context, name string, cb Callback) error {
	ptr, err := i.copyString(ctx, name)
	if err != nil {
		return err
	}
	defer i.freeString(ctx, ptr, uint32(len(name)))

	results, err := i.currentMod.ExportedFunction("runtime_bind_function").
		Call(ctx, i.handle, api.EncodeU32(ptr))
	if err != nil {
		return err
	}
	handle := api.DecodeI32(results[0])
	i.callbacks[handle] = boundCallback{
		name:     name,
		callback: cb,
	}
	return nil
}

// Eval implements Sandbox.
func (i *impl) Eval(ctx context.Context, script string) (json.RawMessage, error) {
	ctx = context.WithValue(ctx, implKey, i)
	scriptPtr, err := i.copyString(ctx, script)
	if err != nil {
		return nil, err
	}
	defer i.freeString(ctx, scriptPtr, uint32(len(script)))

	// Allocate space for output pointer
	outputPtrPtr, err := i.currentMod.ExportedFunction("runtime_malloc_memory").
		Call(ctx, i.handle, api.EncodeU32(4)) // 4 bytes for pointer
	if err != nil {
		return nil, err
	}
	outputPtrAddr := api.DecodeU32(outputPtrPtr[0])
	defer i.currentMod.ExportedFunction("runtime_free_memory").
		Call(ctx, i.handle, api.EncodeU32(outputPtrAddr), api.EncodeU32(4))

	results, err := i.currentMod.ExportedFunction("runtime_eval").
		Call(ctx, i.handle, api.EncodeU32(scriptPtr), api.EncodeU32(outputPtrAddr))
	if err != nil {
		return nil, err
	}

	resultLen := api.DecodeI32(results[0])
	if resultLen == 0 {
		return nil, errors.New("evaluation error: no output")
	}

	// Read output pointer
	outputPtr, ok := i.currentMod.Memory().ReadUint32Le(outputPtrAddr)
	if !ok || outputPtr == 0 {
		return nil, errors.New("evaluation error: failed to read output")
	}

	if resultLen < 0 {
		defer i.freeString(ctx, outputPtr, uint32(-resultLen))
		data, err := i.readStrView(outputPtr)
		if err != nil {
			return nil, err
		}
		return nil, errors.New(string(data))
	}
	defer i.freeString(ctx, outputPtr, uint32(resultLen))
	data, err := i.readStrView(outputPtr)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(data), nil
}

// Reset implements Sandbox.
func (i *impl) Reset(ctx context.Context) error {
	// Save bound functions before reset
	oldCallbacks := i.callbacks

	if err := i.currentMod.Close(ctx); err != nil {
		return err
	}
	mod, err := i.rt.InstantiateModule(ctx, i.compiledMod, i.modConfig)
	if err != nil {
		return err
	}
	results, err := mod.ExportedFunction("runtime_init").Call(ctx)
	if err != nil {
		return err
	}
	i.currentMod = mod
	i.handle = results[0]

	// Clear and re-bind all functions
	i.callbacks = make(map[int32]boundCallback, len(oldCallbacks))
	for _, boundCb := range oldCallbacks {
		if err := i.Bind(ctx, boundCb.name, boundCb.callback); err != nil {
			return err
		}
	}

	return nil
}

// Close implements Sandbox.
func (i *impl) Close(ctx context.Context) error {
	return i.rt.Close(ctx)
}

// readStrView reads a null-terminated C string from WASM memory at the given pointer.
// It searches for the null terminator and returns the string bytes without the terminator.
func (i *impl) readStrView(ptr uint32) ([]byte, error) {
	mem := i.currentMod.Memory()
	size := mem.Size()
	if size == 0 {
		size = math.MaxUint32
	}
	data, ok := mem.Read(ptr, size-ptr)
	if !ok {
		return nil, errors.New("out of memory error")
	}
	idx := bytes.IndexByte(data, 0)
	if idx < 0 {
		return nil, errors.New("invalid c str")
	}
	return data[:idx], nil
}

// copyString allocates memory in the WASM linear memory and copies a Go string into it.
// Returns a pointer to the allocated null-terminated string in WASM memory.
// The caller is responsible for freeing the memory using freeString.
func (i *impl) copyString(ctx context.Context, str string) (uint32, error) {
	// Allocate len(str) + 1 for null terminator
	results, err := i.currentMod.ExportedFunction("runtime_malloc_memory").
		Call(ctx, i.handle, api.EncodeU32(uint32(len(str)+1)))
	if err != nil {
		return 0, err
	}
	ptr := api.DecodeU32(results[0])
	// Write the string
	if ptr == 0 || !i.currentMod.Memory().WriteString(ptr, str) {
		return 0, errors.New("out of memory error")
	}
	// Write null terminator
	if !i.currentMod.Memory().WriteByte(ptr+uint32(len(str)), 0) {
		return 0, errors.New("out of memory error")
	}
	return ptr, nil
}

// freeString releases memory previously allocated in WASM linear memory.
// Safe to call with a zero pointer (no-op).
func (i *impl) freeString(ctx context.Context, ptr uint32, len uint32) error {
	if ptr == 0 {
		return nil
	}
	_, err := i.currentMod.ExportedFunction("runtime_free_memory").
		Call(ctx, i.handle, api.EncodeU32(ptr), api.EncodeU32(len+1))
	return err
}

// precompiledInterpreter is the concrete implementation of the Interpreter interface.
// It holds compiled WASM modules that can be instantiated multiple times to create
// sandboxes efficiently without recompiling.
//
// Fields:
//   - wasip1Mod: Compiled WASI preview1 module for system interface support
//   - hostMod: Compiled host module providing the host_call bridge function
//   - mod: Compiled language runtime WASM module (QuickJS for JavaScript, RustPython for Python)
//   - cache: Compilation cache for performance optimization
//   - cfg: Runtime configuration shared by all sandboxes created from this interpreter
type precompiledInterpreter struct {
	wasip1Mod wazero.CompiledModule
	hostMod   wazero.CompiledModule
	mod       wazero.CompiledModule
	cache     wazero.CompilationCache
	cfg       wazero.RuntimeConfig
}

var _ Interpreter = (*precompiledInterpreter)(nil)

// Close implements Interpreter.
func (p *precompiledInterpreter) Close(ctx context.Context) error {
	return errors.Join(
		p.cache.Close(ctx),
		p.mod.Close(ctx),
		p.hostMod.Close(ctx),
		p.wasip1Mod.Close(ctx),
	)
}

// hostModule implements Interpreter.
func (p *precompiledInterpreter) hostModule() wazero.CompiledModule {
	return p.hostMod
}

// module implements Interpreter.
func (p *precompiledInterpreter) module() wazero.CompiledModule {
	return p.mod
}

// runtimeConfig implements Interpreter.
func (p *precompiledInterpreter) runtimeConfig() wazero.RuntimeConfig {
	return p.cfg
}

// wasip1Module implements Interpreter.
func (p *precompiledInterpreter) wasip1Module() wazero.CompiledModule {
	return p.wasip1Mod
}

// NewInterpreter creates a new interpreter by compiling the provided language runtime WASM binary.
//
// This function performs the expensive one-time compilation of a language runtime (e.g., QuickJS
// for JavaScript, RustPython for Python) and sets up the host function bridge. The resulting
// Interpreter can be reused to create multiple Sandbox instances efficiently.
//
// The compilation process includes:
//  1. Setting up a compilation cache for faster subsequent instantiations
//  2. Compiling the WASI preview1 module for system interface support
//  3. Creating a host module with the host_call function for Go↔runtime communication
//  4. Compiling the language runtime WASM module
//
// Parameters:
//   - ctx: Context for cancellation
//   - wasmBinary: The compiled language runtime WASM module
//
// Returns:
//   - Interpreter: A compiled interpreter ready to create sandboxes
//   - error: Any error that occurred during compilation
//
// Example:
//
//	wasmBinary, _ := os.ReadFile("runtime.wasm")
//	interp, err := codesandbox.NewInterpreter(ctx, wasmBinary)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	defer interp.Close(ctx)
//
//	// Create multiple sandboxes from the same interpreter
//	sandbox1, _ := codesandbox.NewSandbox(ctx, interp)
//	sandbox2, _ := codesandbox.NewSandbox(ctx, interp)
//
// For most use cases, use language-specific packages that embed the WASM binary:
//   - javascript.NewInterpreter() for JavaScript (QuickJS)
//   - python.NewInterpreter() for Python (RustPython)
func NewInterpreter(ctx context.Context, wasmBinary []byte) (Interpreter, error) {
	cache := wazero.NewCompilationCache()
	rtCfg := wazero.NewRuntimeConfig().
		WithCompilationCache(cache).
		WithCloseOnContextDone(true).
		WithCoreFeatures(api.CoreFeaturesV2)
	rt := wazero.NewRuntimeWithConfig(ctx, rtCfg)
	defer rt.Close(ctx)
	wasip1Mod, err := wasi_snapshot_preview1.NewBuilder(rt).Compile(ctx)
	if err != nil {
		return nil, errors.Join(err, cache.Close(ctx))
	}
	// Create the host module with the host_call function, which is the bridge
	// that allows sandboxed code to call Go functions. When a bound function is
	// called from the sandbox, it invokes host_call(function_id, json_payload_ptr, output_ptr_ptr),
	// and this Go function handles the call by looking up the callback and executing it.
	// Returns: positive length for success (JSON), negative length for error (plain text).
	hostMod, err := rt.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithGoModuleFunction(
			api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
				functionID := api.DecodeI32(stack[0])
				jsonPayloadPtr := api.DecodeU32(stack[1])
				outputPtrPtr := api.DecodeU32(stack[2])
				impl, ok := ctx.Value(implKey).(*impl)
				if !ok {
					stack[0] = 0
					return
				}
				writeOutput := func(data []byte, isError bool) int32 {
					ptr, err := impl.copyString(ctx, unsafe.String(&data[0], len(data)))
					if err != nil {
						return 0
					}
					// Write pointer to output location
					if !mod.Memory().WriteUint32Le(outputPtrPtr, ptr) {
						return 0
					}
					length := int32(len(data))
					if isError {
						return -length
					}
					return length
				}

				boundCb, ok := impl.callbacks[functionID]
				if !ok {
					stack[0] = api.EncodeI32(writeOutput([]byte("callback not found"), true))
					return
				}

				data, err := impl.readStrView(jsonPayloadPtr)
				if err != nil {
					stack[0] = api.EncodeI32(writeOutput([]byte("failed to read input: "+err.Error()), true))
					return
				}

				result, err := boundCb.callback(json.RawMessage(data))
				if err != nil {
					stack[0] = api.EncodeI32(writeOutput([]byte(err.Error()), true))
					return
				}

				stack[0] = api.EncodeI32(writeOutput(result, false))
			}),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{api.ValueTypeI32},
		).
		Export("host_call").
		Compile(ctx)
	if err != nil {
		return nil, errors.Join(err, wasip1Mod.Close(ctx), cache.Close(ctx))
	}
	mod, err := rt.CompileModule(ctx, wasmBinary)
	if err != nil {
		return nil, errors.Join(err, wasip1Mod.Close(ctx), hostMod.Close(ctx), cache.Close(ctx))
	}

	// Validate ABI: check that all required functions exist with correct signatures
	if err := validateABI(mod); err != nil {
		return nil, errors.Join(err, mod.Close(ctx), wasip1Mod.Close(ctx), hostMod.Close(ctx), cache.Close(ctx))
	}

	return &precompiledInterpreter{
		wasip1Mod,
		hostMod,
		mod,
		cache,
		rtCfg,
	}, nil
}

// validateABI checks that the WASM module exports all required functions with correct signatures
func validateABI(mod wazero.CompiledModule) error {
	// Define required functions with their expected signatures
	type funcSig struct {
		params  []api.ValueType
		results []api.ValueType
	}

	i32 := api.ValueTypeI32
	required := map[string]funcSig{
		"runtime_init": {
			params:  []api.ValueType{},
			results: []api.ValueType{i32}, // returns pointer (u32)
		},
		"runtime_bind_function": {
			params:  []api.ValueType{i32, i32}, // (handle, name_ptr)
			results: []api.ValueType{i32},      // returns function_id
		},
		"runtime_eval": {
			params:  []api.ValueType{i32, i32, i32}, // (handle, script_ptr, output_ptr)
			results: []api.ValueType{i32},           // returns length (positive) or -length (error)
		},
		"runtime_free": {
			params:  []api.ValueType{i32}, // (handle)
			results: []api.ValueType{},    // void
		},
		"runtime_malloc_memory": {
			params:  []api.ValueType{i32, i32}, // (handle, size)
			results: []api.ValueType{i32},      // returns pointer
		},
		"runtime_free_memory": {
			params:  []api.ValueType{i32, i32, i32}, // (handle, ptr, size)
			results: []api.ValueType{},              // void
		},
	}

	for name, expectedSig := range required {
		def := mod.ExportedFunctions()[name]
		if def == nil {
			return errors.New("ABI validation failed: missing required function: " + name)
		}

		actualParams := def.ParamTypes()
		actualResults := def.ResultTypes()

		// Validate parameter types
		if !slices.Equal(actualParams, expectedSig.params) {
			return errors.New("ABI validation failed: function " + name + " has incorrect parameter types")
		}

		// Validate result types
		if !slices.Equal(actualResults, expectedSig.results) {
			return errors.New("ABI validation failed: function " + name + " has incorrect result types")
		}
	}

	return nil
}

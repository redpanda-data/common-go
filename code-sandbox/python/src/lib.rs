use anyhow::{Context, Result, anyhow};
use rustpython_vm::{self as vm, py_serde};
use std::collections::HashMap;
use std::ffi::{CStr, CString};
use std::os::raw::{c_char, c_int};
use std::ptr;

/// SAFETY: The runtime environment must be single-threaded WASM.
#[global_allocator]
static ALLOCATOR: talc::TalckWasm = unsafe { talc::TalckWasm::new_global() };

/// PythonRuntime structure
///
/// Encapsulates the RustPython interpreter along with bound host functions.
/// - interpreter: The RustPython VM instance
/// - next_function_id: Counter for assigning function IDs to bound host functions
/// - bound_functions: Map of function IDs to their names (for callback resolution)
pub struct PythonRuntime {
    interpreter: vm::Interpreter,
    next_function_id: i32,
    bound_functions: HashMap<i32, String>,
}

/// Converts a Python object to a JSON value
fn py_to_json(obj: vm::PyObjectRef, vm: &vm::VirtualMachine) -> Result<serde_json::Value> {
    let val = py_serde::serialize(vm, &obj, serde_json::value::Serializer)?;
    Ok(val)
}

/// Converts a JSON value to a Python object
fn json_to_py(value: serde_json::Value, vm: &vm::VirtualMachine) -> Result<vm::PyObjectRef> {
    let py_obj = py_serde::deserialize(vm, value)?;
    Ok(py_obj)
}

/// Invokes the host_call function with JSON serialization
fn invoke_host_call(
    function_id: i32,
    payload: serde_json::Value,
) -> Result<serde_json::Value, String> {
    let json_str = payload.to_string();
    let c_json = CString::new(json_str).map_err(|_| "Invalid JSON string".to_string())?;
    let mut output: *mut c_char = ptr::null_mut();

    // Call host_call
    let result_len = unsafe {
        host_call(
            function_id,
            c_json.as_ptr(),
            &mut output as *mut *mut c_char,
        )
    };

    if result_len == 0 || output.is_null() {
        return Err("Host function returned no data".to_string());
    }

    // Read the output
    let output_str = unsafe {
        CStr::from_ptr(output)
            .to_str()
            .map_err(|_| "Invalid UTF-8 from host")?
            .to_string()
    };

    // Free the output (host allocated it)
    unsafe {
        let _ = CString::from_raw(output);
    }

    // Check if error (negative length)
    if result_len < 0 {
        return Err(output_str);
    }

    // Parse JSON response
    serde_json::from_str(&output_str).map_err(|e| format!("Failed to parse host response: {}", e))
}

// Import declaration for WASM host functions
// Host must provide: int32_t host_call(int32_t function_id, const char* json_payload, char** output)
// Returns:
//   - Positive: success, output contains JSON result, return value is length
//   - Negative: failure, output contains error string, absolute value is error length
#[cfg(target_arch = "wasm32")]
unsafe extern "C" {
    fn host_call(function_id: i32, json_payload: *const c_char, output: *mut *mut c_char) -> i32;
}

/// runtime_init
///
/// Initializes a new Python runtime environment.
///
/// Returns:
///   - Pointer to PythonRuntime on success
///   - NULL on failure
///
/// The caller must call runtime_free() to clean up resources.
#[unsafe(no_mangle)]
pub extern "C" fn runtime_init() -> *mut PythonRuntime {
    let runtime = Box::new(PythonRuntime {
        interpreter: vm::Interpreter::with_init(Default::default(), |_vm| {
            // TODO: embed the stdlib to give full access
            // vm.add_native_modules(rustpython_stdlib::get_module_inits());
        }),
        next_function_id: 0,
        bound_functions: HashMap::new(),
    });

    Box::into_raw(runtime)
}

/// runtime_bind_function
///
/// Binds a WASM-imported host function to the Python global scope.
///
/// Parameters:
///   - runtime: The PythonRuntime instance
///   - name: The name to bind the function to in Python (null-terminated C string)
///
/// Returns:
///   - Function ID (>= 0) on success - this ID will be passed to host_call()
///   - -1 on failure
#[unsafe(no_mangle)]
pub extern "C" fn runtime_bind_function(runtime: *mut PythonRuntime, name: *const c_char) -> c_int {
    if runtime.is_null() || name.is_null() {
        return -1;
    }

    let runtime = unsafe { &mut *runtime };
    let name_str = match unsafe { CStr::from_ptr(name) }.to_str() {
        Ok(s) => s,
        Err(_) => return -1,
    };

    let function_id = runtime.next_function_id;
    runtime.next_function_id += 1;

    // Store the function mapping
    runtime
        .bound_functions
        .insert(function_id, name_str.to_string());

    function_id
}

/// runtime_eval
///
/// Evaluates a Python script and returns the result as a JSON string.
///
/// Parameters:
///   - runtime: The PythonRuntime instance
///   - script: The Python code to evaluate (null-terminated string)
///   - output: Pointer to receive the result string (caller must free using runtime_free_memory)
///
/// Returns:
///   - Positive: success, output contains JSON result, return value is length
///   - Negative: failure, output contains error string, absolute value is error length
///   - Zero: invalid parameters
#[unsafe(no_mangle)]
pub extern "C" fn runtime_eval(
    runtime: *mut PythonRuntime,
    script: *const c_char,
    output: *mut *mut c_char,
) -> c_int {
    if runtime.is_null() || script.is_null() || output.is_null() {
        return 0;
    }

    let runtime = unsafe { &mut *runtime };
    let script_str = match unsafe { CStr::from_ptr(script) }.to_str() {
        Ok(s) => s,
        Err(_) => return 0,
    };

    let result: Result<String> = runtime.interpreter.enter(|vm| {
        let scope = vm.new_scope_with_builtins();

        // Bind host functions to the global scope
        for (&function_id, name) in runtime.bound_functions.iter() {
            // Create a Python callable that bridges to host_call
            let func = vm.new_function(
                name,
                move |input: vm::PyObjectRef, vm: &vm::VirtualMachine| -> vm::PyResult {
                    // Serialize the input argument to JSON
                    let json_payload = match py_to_json(input, vm) {
                        Ok(json) => json,
                        Err(e) => {
                            return Err(
                                vm.new_type_error(format!("Failed to serialize argument: {}", e))
                            );
                        }
                    };

                    // Call the host function
                    let json_result = match invoke_host_call(function_id, json_payload) {
                        Ok(result) => result,
                        Err(e) => {
                            return Err(vm.new_runtime_error(format!("Host function error: {}", e)));
                        }
                    };

                    // Deserialize the JSON response back to a Python object
                    json_to_py(json_result, vm).map_err(|e| {
                        vm.new_runtime_error(format!("Failed to deserialize result: {}", e))
                    })
                },
            );

            // Bind the function to the global scope
            scope.globals.set_item(name, func.into(), vm).map_err(|e| {
                let mut msg = String::new();
                vm.write_exception(&mut msg, &e).unwrap();
                anyhow!(msg)
            })?;
        }

        // Compile and execute the script
        let syntax = vm::compiler::parser::parse_module(script_str)?.into_syntax();
        let source_file = vm::compiler::core::SourceFileBuilder::new("<exec>", script_str).finish();
        let code_obj = vm::compiler::codegen::compile::compile_program_single(
            &syntax,
            source_file,
            vm.compile_opts(),
        )
        .map(|code| vm.ctx.new_code(code))?;
        let result = vm.run_code_obj(code_obj, scope).map_err(|e| {
            let mut msg = String::new();
            vm.write_exception(&mut msg, &e).unwrap();
            anyhow!(msg)
        })?;

        // Convert the Python result to JSON
        let json_value = py_to_json(result, vm)?;
        let json_str = serde_json::to_string(&json_value).context("Failed to serialize result")?;

        Ok(json_str)
    });

    match result {
        Ok(json_str) => {
            // Allocate output string
            match CString::new(json_str.as_bytes()) {
                Ok(c_str) => {
                    unsafe {
                        *output = c_str.into_raw();
                    }
                    json_str.len() as c_int
                }
                Err(_) => 0,
            }
        }
        Err(error_msg) => {
            // Return error string
            match CString::new(error_msg.to_string()) {
                Ok(c_str) => {
                    let len = c_str.as_bytes().len() as c_int;
                    unsafe {
                        *output = c_str.into_raw();
                    }
                    -len
                }
                Err(_) => 0,
            }
        }
    }
}

/// runtime_free
///
/// Frees all resources associated with a PythonRuntime instance.
///
/// Parameters:
///   - runtime: The PythonRuntime instance to free (can be NULL)
#[unsafe(no_mangle)]
pub extern "C" fn runtime_free(runtime: *mut PythonRuntime) {
    if !runtime.is_null() {
        unsafe {
            let _ = Box::from_raw(runtime);
        }
    }
}

/// runtime_malloc_memory
///
/// Allocates memory that can be used for data exchange between WASM and host.
///
/// Parameters:
///   - runtime: The PythonRuntime instance
///   - size: Number of bytes to allocate
///
/// Returns:
///   - Pointer to allocated memory
///   - NULL on failure
#[unsafe(no_mangle)]
pub extern "C" fn runtime_malloc_memory(_runtime: *mut PythonRuntime, size: usize) -> *mut u8 {
    if size == 0 {
        return ptr::null_mut();
    }
    let layout = std::alloc::Layout::from_size_align(size, 1).unwrap();
    unsafe { std::alloc::alloc(layout) }
}

/// runtime_free_memory
///
/// Frees memory previously allocated by runtime_malloc_memory.
///
/// Parameters:
///   - runtime: The PythonRuntime instance
///   - ptr: Pointer to memory to free
#[unsafe(no_mangle)]
pub extern "C" fn runtime_free_memory(_runtime: *mut PythonRuntime, ptr: *mut u8, size: usize) {
    if ptr.is_null() {
        return;
    }
    let layout = std::alloc::Layout::from_size_align(size, 1).unwrap();
    unsafe { std::alloc::dealloc(ptr, layout) }
}

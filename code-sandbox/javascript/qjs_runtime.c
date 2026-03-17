/*
 * QuickJS Runtime Wrapper Library
 *
 * This library provides a simplified interface for embedding QuickJS in
 * applications, with functions exported for WebAssembly usage.
 *
 * Main features:
 * - Runtime initialization and cleanup
 * - Binding C functions to JavaScript global scope
 * - Evaluating JavaScript code with JSON-serialized results
 * - Minimal console.log support (no full std/os modules)
 */

#include "quickjs/quickjs.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

// Export functions for WASM with default visibility
#define WASM_EXPORT __attribute__((visibility("default")))

/*
 * Minimal console.log implementation
 */
static JSValue js_console_log(JSContext *ctx, JSValueConst this_val, int argc,
                              JSValueConst *argv) {
  for (int i = 0; i < argc; i++) {
    if (i != 0) {
      putchar(' ');
    }
    const char *str = JS_ToCString(ctx, argv[i]);
    if (str) {
      fputs(str, stdout);
      JS_FreeCString(ctx, str);
    }
  }
  putchar('\n');
  fflush(stdout);
  return JS_UNDEFINED;
}

/*
 * Setup minimal console object with log method
 */
static void setup_console(JSContext *ctx) {
  JSValue global = JS_GetGlobalObject(ctx);
  JSValue console = JS_NewObject(ctx);

  JS_SetPropertyStr(ctx, console, "log",
                    JS_NewCFunction(ctx, js_console_log, "log", 1));

  JS_SetPropertyStr(ctx, global, "console", console);
  JS_FreeValue(ctx, global);
}

/*
 * Maximum number of compiled scripts per runtime instance
 */
#define MAX_COMPILED_SCRIPTS 256

/*
 * QJSRuntime structure
 *
 * Encapsulates both the JSRuntime and JSContext for simplified API usage.
 * - rt: The QuickJS runtime instance
 * - ctx: The JavaScript execution context
 * - next_function_id: Counter for assigning function IDs
 * - compiled_scripts: Array of compiled bytecode values (NULL if slot is free)
 * - next_compiled_id: Next ID to assign to a compiled script
 */
typedef struct {
  JSRuntime *rt;
  JSContext *ctx;
  int next_function_id;
  JSValue *compiled_scripts[MAX_COMPILED_SCRIPTS];
  int next_compiled_id;
} QJSRuntime;

/*
 * runtime_init
 *
 * Initializes a new QuickJS runtime environment with minimal console.log
 * support. Does not include std/os modules to keep the runtime lightweight.
 *
 * Returns:
 *   - Pointer to QJSRuntime on success
 *   - NULL on failure (memory allocation or runtime creation failed)
 *
 * The caller must call runtime_free() to clean up resources.
 */
WASM_EXPORT
QJSRuntime *runtime_init(void) {
  QJSRuntime *runtime = malloc(sizeof(QJSRuntime));
  if (!runtime) {
    return NULL;
  }

  runtime->rt = JS_NewRuntime();
  if (!runtime->rt) {
    free(runtime);
    return NULL;
  }

  runtime->ctx = JS_NewContext(runtime->rt);
  if (!runtime->ctx) {
    JS_FreeRuntime(runtime->rt);
    free(runtime);
    return NULL;
  }

  // Initialize function ID counter
  runtime->next_function_id = 0;

  // Initialize compiled scripts storage
  runtime->next_compiled_id = 0;
  memset(runtime->compiled_scripts, 0, sizeof(runtime->compiled_scripts));

  // Setup minimal console.log support
  setup_console(runtime->ctx);

  return runtime;
}

// Import declaration for WASM host functions
// Host must provide: int32_t host_call(int32_t function_id, const char*
// json_payload, char** output) Returns:
//   - Positive: success, output contains JSON result, return value is length
//   - Negative: failure, output contains error string, absolute value is error
//   length
#ifdef __wasm__
__attribute__((import_module("env"), import_name("host_call")))
#endif
extern int host_call(int function_id, const char *json_payload, char **output);

/*
 * Internal wrapper that bridges JavaScript calls to WASM host_call
 * magic parameter contains the function ID
 */
static JSValue js_bound_function_wrapper(JSContext *ctx, JSValueConst this_val,
                                         int argc, JSValueConst *argv,
                                         int magic, JSValue *func_data) {
  // magic contains the function ID
  int function_id = magic;

  // Convert the single argument to JSON
  const char *json_payload = "null";
  char *allocated_json = NULL;

  if (argc > 0) {
    // Use JSON.stringify to convert the argument
    JSValue global = JS_GetGlobalObject(ctx);
    JSValue json_obj = JS_GetPropertyStr(ctx, global, "JSON");
    JSValue stringify_func = JS_GetPropertyStr(ctx, json_obj, "stringify");

    JSValue json_val = JS_Call(ctx, stringify_func, json_obj, 1, &argv[0]);

    if (!JS_IsException(json_val)) {
      json_payload = JS_ToCString(ctx, json_val);
      if (json_payload) {
        allocated_json = strdup(json_payload);
        JS_FreeCString(ctx, json_payload);
        json_payload = allocated_json;
      }
    }

    JS_FreeValue(ctx, json_val);
    JS_FreeValue(ctx, stringify_func);
    JS_FreeValue(ctx, json_obj);
    JS_FreeValue(ctx, global);
  }

  // Call the host function via WASM import with function ID
  char *output = NULL;
  int result_len = host_call(function_id, json_payload, &output);

  if (allocated_json) {
    free(allocated_json);
  }

  if (result_len == 0 || !output) {
    return JS_ThrowInternalError(ctx, "Host function returned no data");
  }

  // Check if the result is an error (negative length)
  if (result_len < 0) {
    // Error: output contains error string
    JSValue exception = JS_ThrowInternalError(ctx, "%s", output);
    free(output);
    return exception;
  }

  // Success: parse the result JSON
  JSValue result = JS_ParseJSON(ctx, output, result_len, "<host_result>");
  free(output);

  if (JS_IsException(result)) {
    return JS_ThrowInternalError(ctx, "Failed to parse host function result");
  }

  return result;
}

/*
 * runtime_bind_function
 *
 * Binds a WASM-imported host function to the JavaScript global scope.
 *
 * Parameters:
 *   - runtime: The QJSRuntime instance
 *   - name: The name to bind the function to in JavaScript
 *
 * The bound JavaScript function will:
 *   1. Take a single argument (any JSON-serializable value)
 *   2. Serialize it to JSON
 *   3. Call host_call(function_id, json_payload) via WASM import
 *   4. Parse the returned JSON
 *   5. Throw an exception if {"success": false, "error": "message"} is returned
 *   6. Otherwise return the parsed JSON result
 *
 * Returns:
 *   - Function ID (>= 0) on success - this ID will be passed to host_call()
 *   - -1 on failure (invalid parameters or binding failed)
 *
 * Example:
 *   int fetchData_id = runtime_bind_function(runtime, "fetchData");
 *   // JavaScript can now call: fetchData({url: "https://foo.com"})
 *   // Which will call: host_call(fetchData_id, '{"url":"https://foo.com"}')
 */
WASM_EXPORT
int runtime_bind_function(QJSRuntime *runtime, const char *name) {
  if (!runtime || !runtime->ctx || !name) {
    return -1;
  }

  // Assign a unique function ID
  int function_id = runtime->next_function_id++;

  JSValue global = JS_GetGlobalObject(runtime->ctx);

  // Create function with the function ID stored in the magic parameter
  JSValue func_val = JS_NewCFunctionData(
      runtime->ctx, js_bound_function_wrapper, 1, function_id, 0, NULL);

  int ret = JS_SetPropertyStr(runtime->ctx, global, name, func_val);
  JS_FreeValue(runtime->ctx, global);

  // Return the function ID on success, -1 on failure
  return ret < 0 ? -1 : function_id;
}

/*
 * runtime_eval
 *
 * Evaluates a JavaScript script and returns the result as a JSON string.
 *
 * Parameters:
 *   - runtime: The QJSRuntime instance
 *   - script: The JavaScript code to evaluate (null-terminated string)
 *   - output: Pointer to receive the result string (caller must free)
 *
 * Returns:
 *   - Positive: success, output contains JSON result, return value is length
 *   - Negative: failure, output contains error string, absolute value is error
 * length
 *   - Zero: invalid parameters or memory allocation failure
 *
 * Example results:
 *   - "2 + 2" returns 1, output: "4"
 *   - "[1, 2, 3]" returns 9, output: "[1,2,3]"
 *   - "({ x: 10 })" returns 8, output: "{\"x\":10}"
 *   - "invalid syntax" returns negative, output: "SyntaxError: ..."
 */
WASM_EXPORT
int runtime_eval(QJSRuntime *runtime, const char *script, char **output) {
  if (!runtime || !runtime->ctx || !script || !output) {
    return 0;
  }

  *output = NULL;

  // Evaluate the script
  JSValue result = JS_Eval(runtime->ctx, script, strlen(script), "<eval>",
                           JS_EVAL_TYPE_GLOBAL);

  // Check for exceptions
  if (JS_IsException(result)) {
    JSValue exception = JS_GetException(runtime->ctx);
    const char *error_str = JS_ToCString(runtime->ctx, exception);

    // Return plain error string (not JSON)
    if (error_str) {
      *output = strdup(error_str);
      int len = strlen(error_str);
      JS_FreeCString(runtime->ctx, error_str);
      JS_FreeValue(runtime->ctx, exception);
      JS_FreeValue(runtime->ctx, result);
      return -len; // Negative indicates error
    }

    JS_FreeCString(runtime->ctx, error_str);
    JS_FreeValue(runtime->ctx, exception);
    JS_FreeValue(runtime->ctx, result);
    return 0;
  }

  // Convert result to JSON string using JSON.stringify()
  JSValue json_stringify = JS_GetGlobalObject(runtime->ctx);
  JSValue json_obj = JS_GetPropertyStr(runtime->ctx, json_stringify, "JSON");
  JSValue stringify_func =
      JS_GetPropertyStr(runtime->ctx, json_obj, "stringify");

  JSValue json_result =
      JS_Call(runtime->ctx, stringify_func, json_obj, 1, &result);

  int result_len = 0;
  if (!JS_IsException(json_result)) {
    const char *str = JS_ToCString(runtime->ctx, json_result);
    if (str) {
      *output = strdup(str);
      result_len = strlen(str);
      JS_FreeCString(runtime->ctx, str);
    }
  }

  // Clean up all temporary values
  JS_FreeValue(runtime->ctx, json_result);
  JS_FreeValue(runtime->ctx, stringify_func);
  JS_FreeValue(runtime->ctx, json_obj);
  JS_FreeValue(runtime->ctx, json_stringify);
  JS_FreeValue(runtime->ctx, result);

  return result_len; // Positive indicates success
}

/*
 * qjs_runtime_free
 *
 * Frees all resources associated with a QJSRuntime instance.
 *
 * Parameters:
 *   - runtime: The QJSRuntime instance to free (can be NULL)
 *
 * This function properly cleans up:
 *   - The JavaScript context
 *   - The runtime instance
 *   - The QJSRuntime wrapper structure
 *
 * After calling this function, the runtime pointer is invalid and must not be
 * used.
 */
WASM_EXPORT
void runtime_free(QJSRuntime *runtime) {
  if (!runtime) {
    return;
  }

  // Free all compiled scripts
  for (int i = 0; i < MAX_COMPILED_SCRIPTS; i++) {
    if (runtime->compiled_scripts[i]) {
      JS_FreeValue(runtime->ctx, *runtime->compiled_scripts[i]);
      free(runtime->compiled_scripts[i]);
      runtime->compiled_scripts[i] = NULL;
    }
  }

  if (runtime->ctx) {
    JS_FreeContext(runtime->ctx);
  }

  if (runtime->rt) {
    JS_FreeRuntime(runtime->rt);
  }

  free(runtime);
}

/*
 * runtime_compile
 *
 * Compiles a JavaScript script into bytecode without executing it.
 * The compiled bytecode can be executed multiple times via runtime_exec.
 *
 * Parameters:
 *   - runtime: The QJSRuntime instance
 *   - script: The JavaScript code to compile (null-terminated string)
 *   - output: Pointer to receive the compiled script handle (on success)
 *             or error string (on failure)
 *
 * Returns:
 *   - Positive (1): success, output contains the compiled handle (as uint32)
 *   - Negative: failure, output contains error string, absolute value is error length
 *   - Zero: invalid parameters
 */
WASM_EXPORT
int runtime_compile(QJSRuntime *runtime, const char *script, char **output) {
  if (!runtime || !runtime->ctx || !script || !output) {
    return 0;
  }

  *output = NULL;

  // Compile only, do not execute
  JSValue bytecode = JS_Eval(runtime->ctx, script, strlen(script), "<compile>",
                             JS_EVAL_TYPE_GLOBAL | JS_EVAL_FLAG_COMPILE_ONLY);

  if (JS_IsException(bytecode)) {
    JSValue exception = JS_GetException(runtime->ctx);
    const char *error_str = JS_ToCString(runtime->ctx, exception);

    if (error_str) {
      *output = strdup(error_str);
      int len = strlen(error_str);
      JS_FreeCString(runtime->ctx, error_str);
      JS_FreeValue(runtime->ctx, exception);
      JS_FreeValue(runtime->ctx, bytecode);
      return -len;
    }

    JS_FreeCString(runtime->ctx, error_str);
    JS_FreeValue(runtime->ctx, exception);
    JS_FreeValue(runtime->ctx, bytecode);
    return 0;
  }

  // Find a free slot
  int slot = runtime->next_compiled_id;
  if (slot >= MAX_COMPILED_SCRIPTS) {
    JS_FreeValue(runtime->ctx, bytecode);
    const char *err = "too many compiled scripts";
    *output = strdup(err);
    return -(int)strlen(err);
  }
  runtime->next_compiled_id++;

  // Store the compiled bytecode on the heap
  JSValue *stored = malloc(sizeof(JSValue));
  if (!stored) {
    JS_FreeValue(runtime->ctx, bytecode);
    return 0;
  }
  *stored = bytecode;
  runtime->compiled_scripts[slot] = stored;

  // Write the handle (slot + 1) as a uint32 to the output pointer
  // We use slot+1 so that 0 is never a valid handle
  *(uint32_t *)output = (uint32_t)(slot + 1);

  return 1; // success
}

/*
 * runtime_exec
 *
 * Executes a previously compiled script and returns the result as JSON.
 *
 * Parameters:
 *   - runtime: The QJSRuntime instance
 *   - compiled_handle: Handle returned by runtime_compile
 *   - output: Pointer to receive the result string (caller must free)
 *
 * Returns:
 *   - Positive: success, output contains JSON result, return value is length
 *   - Negative: failure, output contains error string, absolute value is error length
 *   - Zero: invalid parameters
 */
WASM_EXPORT
int runtime_exec(QJSRuntime *runtime, int compiled_handle, char **output) {
  if (!runtime || !runtime->ctx || !output || compiled_handle <= 0) {
    return 0;
  }

  *output = NULL;

  int slot = compiled_handle - 1;
  if (slot >= MAX_COMPILED_SCRIPTS || !runtime->compiled_scripts[slot]) {
    const char *err = "invalid compiled script handle";
    *output = strdup(err);
    return -(int)strlen(err);
  }

  // Duplicate the bytecode value (JS_EvalFunction consumes its argument)
  JSValue bytecode = JS_DupValue(runtime->ctx, *runtime->compiled_scripts[slot]);

  // Execute the compiled bytecode
  JSValue result = JS_EvalFunction(runtime->ctx, bytecode);

  // Check for exceptions
  if (JS_IsException(result)) {
    JSValue exception = JS_GetException(runtime->ctx);
    const char *error_str = JS_ToCString(runtime->ctx, exception);

    if (error_str) {
      *output = strdup(error_str);
      int len = strlen(error_str);
      JS_FreeCString(runtime->ctx, error_str);
      JS_FreeValue(runtime->ctx, exception);
      JS_FreeValue(runtime->ctx, result);
      return -len;
    }

    JS_FreeCString(runtime->ctx, error_str);
    JS_FreeValue(runtime->ctx, exception);
    JS_FreeValue(runtime->ctx, result);
    return 0;
  }

  // Convert result to JSON string using JSON.stringify()
  JSValue json_stringify = JS_GetGlobalObject(runtime->ctx);
  JSValue json_obj = JS_GetPropertyStr(runtime->ctx, json_stringify, "JSON");
  JSValue stringify_func =
      JS_GetPropertyStr(runtime->ctx, json_obj, "stringify");

  JSValue json_result =
      JS_Call(runtime->ctx, stringify_func, json_obj, 1, &result);

  int result_len = 0;
  if (!JS_IsException(json_result)) {
    const char *str = JS_ToCString(runtime->ctx, json_result);
    if (str) {
      *output = strdup(str);
      result_len = strlen(str);
      JS_FreeCString(runtime->ctx, str);
    }
  }

  // Clean up all temporary values
  JS_FreeValue(runtime->ctx, json_result);
  JS_FreeValue(runtime->ctx, stringify_func);
  JS_FreeValue(runtime->ctx, json_obj);
  JS_FreeValue(runtime->ctx, json_stringify);
  JS_FreeValue(runtime->ctx, result);

  return result_len;
}

/*
 * runtime_free_script
 *
 * Frees a compiled script previously created by runtime_compile.
 *
 * Parameters:
 *   - runtime: The QJSRuntime instance
 *   - compiled_handle: Handle returned by runtime_compile
 */
WASM_EXPORT
void runtime_free_script(QJSRuntime *runtime, int compiled_handle) {
  if (!runtime || !runtime->ctx || compiled_handle <= 0) {
    return;
  }

  int slot = compiled_handle - 1;
  if (slot >= MAX_COMPILED_SCRIPTS || !runtime->compiled_scripts[slot]) {
    return;
  }

  JS_FreeValue(runtime->ctx, *runtime->compiled_scripts[slot]);
  free(runtime->compiled_scripts[slot]);
  runtime->compiled_scripts[slot] = NULL;
}

WASM_EXPORT void *runtime_malloc_memory(QJSRuntime *runtime, size_t amt) {
  return js_malloc_rt(runtime->rt, amt);
}

WASM_EXPORT void runtime_free_memory(QJSRuntime *runtime, void *ptr, size_t _) {
  js_free_rt(runtime->rt, ptr);
}

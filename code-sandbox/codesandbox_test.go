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

package codesandbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/redpanda-data/common-go/code-sandbox"
	"github.com/redpanda-data/common-go/code-sandbox/javascript"
	"github.com/stretchr/testify/require"
)

// TestBasicEval tests basic JavaScript evaluation
func TestBasicEval(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	tests := []struct {
		name     string
		script   string
		expected any
	}{
		{
			name:     "simple arithmetic",
			script:   "2 + 2",
			expected: float64(4),
		},
		{
			name:     "string concatenation",
			script:   `"hello" + " " + "world"`,
			expected: "hello world",
		},
		{
			name:     "object literal",
			script:   `({name: "Alice", age: 30})`,
			expected: map[string]any{"name": "Alice", "age": float64(30)},
		},
		{
			name:     "array operations",
			script:   `[1, 2, 3].map(x => x * 2)`,
			expected: []any{float64(2), float64(4), float64(6)},
		},
		{
			name:     "function definition and call",
			script:   `const add = (a, b) => a + b; add(5, 7)`,
			expected: float64(12),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sandbox.Eval(ctx, tt.script)
			require.NoError(t, err, "Eval failed")

			var actual any
			err = json.Unmarshal(result, &actual)
			require.NoError(t, err, "Failed to unmarshal result")

			require.JSONEq(t, toJSON(tt.expected), toJSON(actual))
		})
	}
}

// TestBindFunction tests binding Go functions and calling them from JavaScript
func TestBindFunction(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// Bind a simple addition function
	err = sandbox.Bind(ctx, "add", func(data json.RawMessage) (json.RawMessage, error) {
		var nums []int
		if err := json.Unmarshal(data, &nums); err != nil {
			return nil, err
		}
		if len(nums) != 2 {
			return nil, errors.New("expected 2 numbers")
		}
		result := nums[0] + nums[1]
		return json.Marshal(result)
	})
	require.NoError(t, err, "Failed to bind function")

	// Call the bound function from JavaScript
	result, err := sandbox.Eval(ctx, `add([10, 32])`)
	require.NoError(t, err, "Eval failed")

	var actual int
	err = json.Unmarshal(result, &actual)
	require.NoError(t, err, "Failed to unmarshal result")

	require.Equal(t, 42, actual)
}

// TestBindFunctionWithComplexData tests binding with complex data structures
func TestBindFunctionWithComplexData(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	type User struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	// Bind a function that returns a complex object
	err = sandbox.Bind(ctx, "getUser", func(data json.RawMessage) (json.RawMessage, error) {
		var userID string
		if err := json.Unmarshal(data, &userID); err != nil {
			return nil, err
		}
		user := User{
			ID:   userID,
			Name: "Alice",
			Age:  30,
		}
		return json.Marshal(user)
	})
	require.NoError(t, err, "Failed to bind function")

	result, err := sandbox.Eval(ctx, `getUser("user-123")`)
	require.NoError(t, err, "Eval failed")

	var actual User
	err = json.Unmarshal(result, &actual)
	require.NoError(t, err, "Failed to unmarshal result")

	require.Equal(t, "user-123", actual.ID)
	require.Equal(t, "Alice", actual.Name)
	require.Equal(t, 30, actual.Age)
}

// TestBindFunctionError tests error handling in bound functions
func TestBindFunctionError(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// Bind a function that returns an error
	err = sandbox.Bind(ctx, "failing", func(data json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("intentional error")
	})
	require.NoError(t, err, "Failed to bind function")

	// When a bound function returns an error, JavaScript should throw an exception
	_, err = sandbox.Eval(ctx, `failing("test")`)
	require.Error(t, err, "Expected error from failing callback")
	require.Contains(t, err.Error(), "intentional error")
}

// TestMultipleSandboxes tests creating multiple isolated sandboxes
func TestMultipleSandboxes(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	// Create first sandbox with a variable
	sandbox1, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox1")
	defer sandbox1.Close(ctx)

	_, err = sandbox1.Eval(ctx, `var x = 42`)
	require.NoError(t, err, "Failed to set variable in sandbox1")

	// Create second sandbox - should have isolated state
	sandbox2, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox2")
	defer sandbox2.Close(ctx)

	// Try to access x from sandbox2 - should fail
	result, err := sandbox2.Eval(ctx, `typeof x`)
	require.NoError(t, err, "Eval failed")

	var actual string
	err = json.Unmarshal(result, &actual)
	require.NoError(t, err, "Failed to unmarshal result")
	require.Equal(t, "undefined", actual, "sandboxes should be isolated")

	// Verify x still exists in sandbox1
	result, err = sandbox1.Eval(ctx, `x`)
	require.NoError(t, err, "Eval failed")

	var x float64
	err = json.Unmarshal(result, &x)
	require.NoError(t, err, "Failed to unmarshal result")
	require.Equal(t, float64(42), x)
}

// TestMemoryLimit tests memory limit configuration
func TestMemoryLimit(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	// Create sandbox with very small memory limit
	sandbox, err := codesandbox.NewSandbox(ctx, interp,
		codesandbox.WithMaxMemory(1*1024*1024)) // 1MB
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// Simple operations should work
	_, err = sandbox.Eval(ctx, `[1, 2, 3].map(x => x * 2)`)
	require.NoError(t, err, "Simple eval should work with memory limit")

	// Note: Actually hitting memory limits in QuickJS is complex and depends on
	// the WASM implementation. This test mainly verifies the option works.
}

// TestContextCancellation tests that context cancellation stops execution
func TestContextCancellation(t *testing.T) {
	interp, err := javascript.NewInterpreter(context.Background())
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(context.Background())

	sandbox, err := codesandbox.NewSandbox(context.Background(), interp,
		codesandbox.WithMaxRuntime(100*time.Millisecond))
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(context.Background())

	// Create a context with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Try to run an infinite loop
	start := time.Now()
	_, err = sandbox.Eval(ctx, `while(true) {}`)
	duration := time.Since(start)

	require.Error(t, err, "Expected error from infinite loop")
	require.Less(t, duration, 2*time.Second, "Expected timeout within ~100ms")
}

// TestJavaScriptError tests that JavaScript errors are propagated to Go
func TestJavaScriptError(t *testing.T) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// This will throw a ReferenceError
	_, err = sandbox.Eval(ctx, `undefinedVariable`)
	require.Error(t, err, "Expected error from undefined variable")
}

// Helper function to convert values to JSON strings for comparison
func toJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

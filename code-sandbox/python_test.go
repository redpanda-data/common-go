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

	"github.com/stretchr/testify/require"

	codesandbox "github.com/redpanda-data/common-go/code-sandbox"
	"github.com/redpanda-data/common-go/code-sandbox/python"
)

// TestPythonBasicEval tests basic Python evaluation
func TestPythonBasicEval(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
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
			name:     "dictionary literal",
			script:   `{"name": "Alice", "age": 30}`,
			expected: map[string]any{"name": "Alice", "age": float64(30)},
		},
		{
			name:     "list operations",
			script:   `[x * 2 for x in [1, 2, 3]]`,
			expected: []any{float64(2), float64(4), float64(6)},
		},
		{
			name:     "function definition and call",
			script:   `(lambda a, b: a + b)(5, 7)`,
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

// TestPythonBindFunction tests binding Go functions and calling them from Python
func TestPythonBindFunction(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
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

	// Call the bound function from Python
	result, err := sandbox.Eval(ctx, `add([10, 32])`)
	require.NoError(t, err, "Eval failed")

	var actual int
	err = json.Unmarshal(result, &actual)
	require.NoError(t, err, "Failed to unmarshal result")

	require.Equal(t, 42, actual)
}

// TestPythonBindFunctionWithComplexData tests binding with complex data structures
func TestPythonBindFunctionWithComplexData(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
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

// TestPythonBindFunctionError tests error handling in bound functions
func TestPythonBindFunctionError(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// Bind a function that returns an error
	err = sandbox.Bind(ctx, "failing", func(_ json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("intentional error")
	})
	require.NoError(t, err, "Failed to bind function")

	// When a bound function returns an error, Python should raise an exception
	_, err = sandbox.Eval(ctx, `failing("test")`)
	require.Error(t, err, "Expected error from failing callback")
	require.Contains(t, err.Error(), "intentional error")
}

// TestPythonMultipleSandboxes tests creating multiple isolated sandboxes
func TestPythonMultipleSandboxes(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	// Create first sandbox with a variable
	sandbox1, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox1")
	defer sandbox1.Close(ctx)

	// Note: Python eval mode doesn't persist variables like exec mode would
	// We'll test that sandboxes have separate bound functions instead
	err = sandbox1.Bind(ctx, "test_func", func(data json.RawMessage) (json.RawMessage, error) {
		return json.Marshal("from sandbox1")
	})
	require.NoError(t, err, "Failed to bind function in sandbox1")

	// Create second sandbox - should have isolated state
	sandbox2, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox2")
	defer sandbox2.Close(ctx)

	// Try to access test_func from sandbox2 - should fail
	_, err = sandbox2.Eval(ctx, `test_func("data")`)
	require.Error(t, err, "sandbox2 should not have access to sandbox1's functions")

	// Verify test_func still works in sandbox1
	result, err := sandbox1.Eval(ctx, `test_func("data")`)
	require.NoError(t, err, "Eval failed in sandbox1")

	var actual string
	err = json.Unmarshal(result, &actual)
	require.NoError(t, err, "Failed to unmarshal result")
	require.Equal(t, "from sandbox1", actual)
}

// TestPythonMemoryLimit tests memory limit configuration
func TestPythonMemoryLimit(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	// Create sandbox with small memory limit
	sandbox, err := codesandbox.NewSandbox(ctx, interp,
		codesandbox.WithMaxMemory(10*1024*1024)) // 10MB
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// Simple operations should work
	_, err = sandbox.Eval(ctx, `[x * 2 for x in [1, 2, 3]]`)
	require.NoError(t, err, "Simple eval should work with memory limit")
}

// TestPythonContextCancellation tests that context cancellation stops execution
func TestPythonContextCancellation(t *testing.T) {
	interp, err := python.NewInterpreter(context.Background())
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
	_, err = sandbox.Eval(ctx, `while True: pass`)
	duration := time.Since(start)

	require.Error(t, err, "Expected error from infinite loop")
	require.Less(t, duration, 2*time.Second, "Expected timeout within ~100ms")
}

// TestPythonError tests that Python errors are propagated to Go
func TestPythonError(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	// This will throw a NameError
	_, err = sandbox.Eval(ctx, `undefined_variable`)
	require.Error(t, err, "Expected error from undefined variable")
}

// TestPythonBooleanValues tests that Python boolean values are correctly serialized
func TestPythonBooleanValues(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	tests := []struct {
		name     string
		script   string
		expected bool
	}{
		{
			name:     "True",
			script:   "True",
			expected: true,
		},
		{
			name:     "False",
			script:   "False",
			expected: false,
		},
		{
			name:     "comparison",
			script:   "5 > 3",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := sandbox.Eval(ctx, tt.script)
			require.NoError(t, err, "Eval failed")

			var actual bool
			err = json.Unmarshal(result, &actual)
			require.NoError(t, err, "Failed to unmarshal result")

			require.Equal(t, tt.expected, actual)
		})
	}
}

// TestPythonNoneValue tests that Python None is correctly serialized to null
func TestPythonNoneValue(t *testing.T) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	require.NoError(t, err, "Failed to create interpreter")
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	require.NoError(t, err, "Failed to create sandbox")
	defer sandbox.Close(ctx)

	result, err := sandbox.Eval(ctx, "None")
	require.NoError(t, err, "Eval failed")

	require.JSONEq(t, "null", string(result))
}

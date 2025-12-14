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
	"testing"

	codesandbox "github.com/redpanda-data/common-go/code-sandbox"
	"github.com/redpanda-data/common-go/code-sandbox/javascript"
	"github.com/redpanda-data/common-go/code-sandbox/python"
)

// BenchmarkJavaScriptNewInterpreter benchmarks creating a JavaScript interpreter
func BenchmarkJavaScriptNewInterpreter(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interp, err := javascript.NewInterpreter(ctx)
		if err != nil {
			b.Fatal(err)
		}
		interp.Close(ctx)
	}
}

// BenchmarkPythonNewInterpreter benchmarks creating a Python interpreter
func BenchmarkPythonNewInterpreter(b *testing.B) {
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		interp, err := python.NewInterpreter(ctx)
		if err != nil {
			b.Fatal(err)
		}
		interp.Close(ctx)
	}
}

// BenchmarkJavaScriptNewSandbox benchmarks creating a JavaScript sandbox
func BenchmarkJavaScriptNewSandbox(b *testing.B) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer interp.Close(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sandbox, err := codesandbox.NewSandbox(ctx, interp)
		if err != nil {
			b.Fatal(err)
		}
		sandbox.Close(ctx)
	}
}

// BenchmarkPythonNewSandbox benchmarks creating a Python sandbox
func BenchmarkPythonNewSandbox(b *testing.B) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer interp.Close(ctx)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sandbox, err := codesandbox.NewSandbox(ctx, interp)
		if err != nil {
			b.Fatal(err)
		}
		sandbox.Close(ctx)
	}
}

// BenchmarkJavaScriptEval benchmarks JavaScript evaluation with dot product computation
func BenchmarkJavaScriptEval(b *testing.B) {
	ctx := context.Background()

	interp, err := javascript.NewInterpreter(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	if err != nil {
		b.Fatal(err)
	}
	defer sandbox.Close(ctx)

	// Bind a function that returns large vectors
	err = sandbox.Bind(ctx, "getVectors", func(data json.RawMessage) (json.RawMessage, error) {
		// Return two vectors of 1000 elements each
		type Vectors struct {
			A []float64 `json:"a"`
			B []float64 `json:"b"`
		}

		vectors := Vectors{
			A: make([]float64, 1000),
			B: make([]float64, 1000),
		}

		for i := 0; i < 1000; i++ {
			vectors.A[i] = float64(i)
			vectors.B[i] = float64(i * 2)
		}

		return json.Marshal(vectors)
	})
	if err != nil {
		b.Fatal(err)
	}

	// JavaScript code that computes dot product
	script := `
		(() => {
			const vectors = getVectors(null);
			let dotProduct = 0;
			for (let i = 0; i < vectors.a.length; i++) {
				dotProduct += vectors.a[i] * vectors.b[i];
			}
			return dotProduct;
		})()
	`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := sandbox.Eval(ctx, script)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPythonEval benchmarks Python evaluation with dot product computation
func BenchmarkPythonEval(b *testing.B) {
	ctx := context.Background()

	interp, err := python.NewInterpreter(ctx)
	if err != nil {
		b.Fatal(err)
	}
	defer interp.Close(ctx)

	sandbox, err := codesandbox.NewSandbox(ctx, interp)
	if err != nil {
		b.Fatal(err)
	}
	defer sandbox.Close(ctx)

	// Bind a function that returns large vectors
	err = sandbox.Bind(ctx, "getVectors", func(data json.RawMessage) (json.RawMessage, error) {
		// Return two vectors of 1000 elements each
		type Vectors struct {
			A []float64 `json:"a"`
			B []float64 `json:"b"`
		}

		vectors := Vectors{
			A: make([]float64, 1000),
			B: make([]float64, 1000),
		}

		for i := 0; i < 1000; i++ {
			vectors.A[i] = float64(i)
			vectors.B[i] = float64(i * 2)
		}

		return json.Marshal(vectors)
	})
	if err != nil {
		b.Fatal(err)
	}

	// Python code that computes dot product
	script := `(lambda v: sum(a * b for a, b in zip(v["a"], v["b"])))(getVectors(None))`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := sandbox.Eval(ctx, script)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Copyright 2026 Redpanda Data, Inc.
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

package log

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/uuid"
)

func TestContextFreeLogger(t *testing.T) {
	var keysAndValues []any

	uuid := uuid.NewUUID()
	testCases := []struct {
		name   string
		input  []any
		output []any
	}{
		{
			name:   "empty",
			input:  []any{},
			output: []any{},
		},
		{
			name: "empty",
			input: []any{
				"ctx",
				t.Context(),
			},
			output: []any{},
		},
		{
			name: "empty",
			input: []any{
				"ctx",
				logr.NewContext(context.Background(), logr.Logger{}),
			},
			output: []any{},
		},
		{
			name: "empty",
			input: []any{
				"ctx",
				t.Context(),
				"reconcileID",
				uuid,
			},
			output: []any{
				"reconcileID",
				uuid,
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			keysAndValues = filterContexts(tc.input)
			require.Equal(t, tc.output, keysAndValues)
		})
	}
}

func TestContextFreeLogSink_WithValues(t *testing.T) {
	var output string
	baseSink := funcr.NewJSON(func(obj string) {
		output = obj
	}, funcr.Options{})

	logger := logr.New(ContextFree(baseSink.GetSink())).
		WithValues("ctx", t.Context(), "reconcileID", "abc-123")

	output = ""
	logger.Info("test message")

	assert.NotEmpty(t, output)
	assert.Contains(t, output, `"reconcileID":"abc-123"`)
	assert.NotContains(t, output, "ctx")

	output = ""
	logger.Error(errors.New("boom"), "error message")

	assert.NotEmpty(t, output)
	assert.Contains(t, output, `"reconcileID":"abc-123"`)
	assert.NotContains(t, output, "ctx")
}

func TestContextFreeLogSink_InfoAndError(t *testing.T) {
	var output string
	baseSink := funcr.NewJSON(func(obj string) {
		output = obj
	}, funcr.Options{})

	logger := logr.New(ContextFree(baseSink.GetSink()))

	output = ""
	logger.Info("inline context", "ctx", t.Context(), "key", "value")

	assert.NotEmpty(t, output)
	assert.Contains(t, output, `"key":"value"`)
	assert.NotContains(t, output, "ctx")

	output = ""
	logger.Error(errors.New("fail"), "inline context", "ctx", t.Context(), "key", "value")

	assert.NotEmpty(t, output)
	assert.Contains(t, output, `"key":"value"`)
	assert.NotContains(t, output, "ctx")
}

func TestContextFreeLogSink_WithName(t *testing.T) {
	var output string
	baseSink := funcr.NewJSON(func(obj string) {
		output = obj
	}, funcr.Options{})

	logger := logr.New(ContextFree(baseSink.GetSink())).
		WithName("mylogger").
		WithValues("ctx", t.Context(), "key", "value")

	output = ""
	logger.Info("named logger")

	assert.NotEmpty(t, output)
	assert.Contains(t, output, `"logger":"mylogger"`)
	assert.Contains(t, output, `"key":"value"`)
	assert.NotContains(t, output, "ctx")
}

func TestContextFreeLogSink_ChainedWithValues(t *testing.T) {
	var output string
	baseSink := funcr.NewJSON(func(obj string) {
		output = obj
	}, funcr.Options{})

	logger := logr.New(ContextFree(baseSink.GetSink())).
		WithValues("ctx", t.Context()).
		WithValues("key1", "val1").
		WithValues("ctx2", context.Background()).
		WithValues("key2", "val2")

	output = ""
	logger.Info("chained")

	assert.NotEmpty(t, output)
	assert.Contains(t, output, `"key1":"val1"`)
	assert.Contains(t, output, `"key2":"val2"`)
	// Verify neither context key leaked through any WithValues call
	for _, ctxKey := range []string{`"ctx"`, `"ctx2"`} {
		assert.False(t, strings.Contains(output, ctxKey),
			"output should not contain context key %s, got: %s", ctxKey, output)
	}
}

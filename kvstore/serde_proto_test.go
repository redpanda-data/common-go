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

package kvstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/sr"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func TestProtoSerde_BasicEncodeDecode(t *testing.T) {
	serde, err := Proto(func() *wrapperspb.StringValue {
		return &wrapperspb.StringValue{}
	})
	require.NoError(t, err)

	original := wrapperspb.String("hello world")

	// Serialize
	data, err := serde.Serialize(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Deserialize
	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, original.GetValue(), decoded.GetValue())
}

func TestProtoSerde_InvalidData(t *testing.T) {
	serde, err := Proto(func() *wrapperspb.StringValue {
		return &wrapperspb.StringValue{}
	})
	require.NoError(t, err)

	_, err = serde.Deserialize([]byte("invalid protobuf data"))
	assert.Error(t, err)
}

func TestFindMessageIndex(t *testing.T) {
	protoContent := `
syntax = "proto3";
package redpanda.api.aigw.v1alpha1;

enum LLMProviderType {
  LLM_PROVIDER_TYPE_UNSPECIFIED = 0;
}

message LLMProvider {
  string name = 1;
}

message CreateLLMProviderRequest {
  LLMProvider llm_provider = 1;
}

message CreateLLMProviderResponse {
  LLMProvider llm_provider = 1;
}
`

	tests := []struct {
		name      string
		msgName   string
		wantIdx   int
		wantError bool
	}{
		{"first message", "LLMProvider", 0, false},
		{"second message", "CreateLLMProviderRequest", 1, false},
		{"third message", "CreateLLMProviderResponse", 2, false},
		{"not found", "DoesNotExist", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := findMessageIndex(protoContent, tt.msgName)
			if tt.wantError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "not found")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantIdx, idx)
			}
		})
	}
}

func TestProtoSerde_WithSchemaRegistry(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	// Start Redpanda with Schema Registry
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redpandadata/redpanda:latest",
			ExposedPorts: []string{"9092/tcp", "8081/tcp"},
			Cmd: []string{
				"redpanda",
				"start",
				"--mode=dev-container",
				"--smp=1",
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("9092/tcp"),
				wait.ForListeningPort("8081/tcp"),
			),
		},
		Started: true,
	})
	require.NoError(t, err)
	defer container.Terminate(ctx)

	// Get Schema Registry endpoint
	srHost, err := container.Host(ctx)
	require.NoError(t, err)
	srPort, err := container.MappedPort(ctx, "8081")
	require.NoError(t, err)

	srURL := fmt.Sprintf("http://%s:%s", srHost, srPort.Port())
	t.Logf("Schema Registry URL: %s", srURL)

	// Create Schema Registry client
	srClient, err := sr.NewClient(sr.URLs(srURL))
	require.NoError(t, err)

	// Define a simple protobuf schema
	schemaContent := `
syntax = "proto3";
package test;

message StringValue {
  string value = 1;
}
`

	// Create serde with Schema Registry
	serde, err := Proto(
		func() *wrapperspb.StringValue {
			return &wrapperspb.StringValue{}
		},
		WithSchemaRegistry(srClient, "test-subject", schemaContent),
	)
	require.NoError(t, err)

	// Test encode/decode with Schema Registry wire format
	original := wrapperspb.String("hello schema registry")

	// Serialize (should include wire format)
	data, err := serde.Serialize(original)
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	// Verify wire format: first byte should be 0x00 (magic byte)
	assert.Equal(t, byte(0x00), data[0], "First byte should be magic byte 0x00")

	// Deserialize (should decode wire format)
	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, original.GetValue(), decoded.GetValue())
}

func TestProtoSerde_WithSchemaRegistry_MessageName(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ctx := context.Background()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redpandadata/redpanda:latest",
			ExposedPorts: []string{"9092/tcp", "8081/tcp"},
			Cmd: []string{
				"redpanda",
				"start",
				"--mode=dev-container",
				"--smp=1",
			},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("9092/tcp"),
				wait.ForListeningPort("8081/tcp"),
			),
		},
		Started: true,
	})
	require.NoError(t, err)
	defer container.Terminate(ctx)

	srHost, err := container.Host(ctx)
	require.NoError(t, err)
	srPort, err := container.MappedPort(ctx, "8081")
	require.NoError(t, err)

	srClient, err := sr.NewClient(sr.URLs(fmt.Sprintf("http://%s:%s", srHost, srPort.Port())))
	require.NoError(t, err)

	// Schema where StringValue is the SECOND message (index 1), not first.
	schemaContent := `
syntax = "proto3";
package test;

message Unused {
  int32 x = 1;
}

message StringValue {
  string value = 1;
}
`
	// WithMessageName resolves "StringValue" to index 1.
	serde, err := Proto(
		func() *wrapperspb.StringValue { return &wrapperspb.StringValue{} },
		WithSchemaRegistry(srClient, "test-msgname-subject", schemaContent,
			WithMessageName("StringValue"),
		),
	)
	require.NoError(t, err)

	original := wrapperspb.String("non-zero index test")
	data, err := serde.Serialize(original)
	require.NoError(t, err)

	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, original.GetValue(), decoded.GetValue())
}

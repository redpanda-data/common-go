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

package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/twmb/franz-go/pkg/sr"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonkvstore "github.com/redpanda-data/common-go/kvstore"
	otherv1 "github.com/redpanda-data/common-go/protoc-gen-go-sr-normalize/example/gen/go/example/other/v1"
	examplev1 "github.com/redpanda-data/common-go/protoc-gen-go-sr-normalize/example/gen/go/example/v1"
)

// TestGeneratedSchemas_SelfContained verifies that generated schemas are
// parseable and self-contained: they don't reference types that aren't
// defined in the schema itself (except well-known google.protobuf imports).
func TestGeneratedSchemas_SelfContained(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		subject string
		// messages that must appear in the schema
		wantMessages []string
		// enums that must appear in the schema
		wantEnums []string
	}{
		{
			name:         "Product",
			schema:       examplev1.ProductSRSchema,
			subject:      examplev1.ProductSRSubject,
			wantMessages: []string{"Product"},
			wantEnums:    []string{"ProductStatus"},
		},
		{
			name:         "Order with cross-file deps",
			schema:       examplev1.OrderSRSchema,
			subject:      examplev1.OrderSRSubject,
			wantMessages: []string{"Order", "OrderItem", "Product"},
			wantEnums:    []string{"OrderStatus", "ProductStatus"},
		},
		{
			name:         "Event with cross-package dep",
			schema:       examplev1.EventSRSchema,
			subject:      examplev1.EventSRSubject,
			wantMessages: []string{"Event"},
			wantEnums:    []string{"ProviderType"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotEmpty(t, tt.schema)
			require.NotEmpty(t, tt.subject)

			// Target message must be first (index 0).
			lines := strings.Split(tt.schema, "\n")
			var firstMsg string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "message ") {
					firstMsg = strings.TrimSuffix(strings.TrimPrefix(line, "message "), " {")
					break
				}
			}
			assert.Equal(t, tt.wantMessages[0], firstMsg,
				"target message must be first (index 0) in the schema")

			// All expected messages present.
			for _, msg := range tt.wantMessages {
				assert.Contains(t, tt.schema, "message "+msg+" {",
					"schema should contain message %s", msg)
			}

			// All expected enums present.
			for _, enum := range tt.wantEnums {
				assert.Contains(t, tt.schema, "enum "+enum+" {",
					"schema should contain enum %s", enum)
			}

			// Must NOT contain services, validation annotations, or HTTP annotations.
			assert.NotContains(t, tt.schema, "service ")
			assert.NotContains(t, tt.schema, "buf.validate")
			assert.NotContains(t, tt.schema, "google.api")
			assert.NotContains(t, tt.schema, "field_behavior")

			// Request/Response wrappers must not leak in.
			assert.NotContains(t, tt.schema, "Request")
			assert.NotContains(t, tt.schema, "Response")
		})
	}
}

// TestGeneratedSchemas_Independent verifies that each generated schema works
// independently -- Product schema doesn't need Order and vice versa.
func TestGeneratedSchemas_Independent(t *testing.T) {
	// Product schema should not reference Order types.
	assert.NotContains(t, examplev1.ProductSRSchema, "Order")
	assert.NotContains(t, examplev1.ProductSRSchema, "OrderItem")
	assert.NotContains(t, examplev1.ProductSRSchema, "OrderStatus")

	// Order schema must inline Product (cross-file dep) but not Product's
	// service or request/response wrappers.
	assert.Contains(t, examplev1.OrderSRSchema, "message Product {")
	assert.NotContains(t, examplev1.OrderSRSchema, "CreateProduct")
	assert.NotContains(t, examplev1.OrderSRSchema, "GetProduct")
}

// TestGeneratedSchemas_CrossPackage verifies that a dependency from a DIFFERENT
// proto package (example.other.v1.ProviderType, referenced by example.v1.Event)
// is inlined as a top-level enum and referenced by its bare local name. A
// partially package-qualified reference like "other.v1.ProviderType" is a
// dangling type that Schema Registry rejects.
func TestGeneratedSchemas_CrossPackage(t *testing.T) {
	schema := examplev1.EventSRSchema

	// The cross-package enum is inlined top-level and referenced by local name.
	assert.Contains(t, schema, "enum ProviderType {")
	assert.Contains(t, schema, "ProviderType provider = 2;")

	// No dangling, partially package-qualified reference may survive.
	assert.NotContains(t, schema, "other.v1.ProviderType")
	assert.NotContains(t, schema, "example.other")
}

// TestLocalTypeName covers the reference-naming logic directly. The cross-package
// case is the regression: the referred type's own package must be stripped, not
// just the prefix it shares with the referrer.
func TestLocalTypeName(t *testing.T) {
	tests := []struct {
		name string
		full string
		pkg  string
		want string
	}{
		{"same package top-level", "example.v1.ProductStatus", "example.v1", "ProductStatus"},
		{"cross package top-level", "example.other.v1.ProviderType", "example.other.v1", "ProviderType"},
		{"nested type keeps parent path", "example.v1.Product.Category", "example.v1", "Product.Category"},
		{"empty package returns full name", "TopLevel", "", "TopLevel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := localTypeName(protoreflect.FullName(tt.full), protoreflect.FullName(tt.pkg))
			assert.Equal(t, tt.want, got)
		})
	}
}

func startRedpanda(t *testing.T) *sr.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "redpandadata/redpanda:latest",
			ExposedPorts: []string{"9092/tcp", "8081/tcp"},
			Cmd:          []string{"redpanda", "start", "--mode=dev-container", "--smp=1"},
			WaitingFor: wait.ForAll(
				wait.ForListeningPort("9092/tcp"),
				wait.ForListeningPort("8081/tcp"),
			),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { container.Terminate(ctx) })

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "8081")
	require.NoError(t, err)

	srClient, err := sr.NewClient(sr.URLs(fmt.Sprintf("http://%s:%s", host, port.Port())))
	require.NoError(t, err)
	return srClient
}

// TestSchemaRegistry_ProductRoundTrip registers the generated Product schema
// with a real Schema Registry, then serializes and deserializes a Product
// through the kvstore serde to verify the full pipeline.
func TestSchemaRegistry_ProductRoundTrip(t *testing.T) {
	srClient := startRedpanda(t)

	serde, err := commonkvstore.Proto(
		func() *examplev1.Product { return &examplev1.Product{} },
		commonkvstore.WithSchemaRegistry(srClient, examplev1.ProductSRSubject, examplev1.ProductSRSchema),
	)
	require.NoError(t, err)

	original := &examplev1.Product{
		Id:        "prod-1",
		Name:      "Widget",
		Status:    examplev1.ProductStatus_PRODUCT_STATUS_ACTIVE,
		Tags:      []string{"sale", "new"},
		CreatedAt: timestamppb.Now(),
	}

	data, err := serde.Serialize(original)
	require.NoError(t, err)

	// Verify Confluent wire format: magic byte 0x00.
	require.Greater(t, len(data), 5, "serialized data too short for wire format")
	assert.Equal(t, byte(0x00), data[0], "first byte must be magic byte 0x00")

	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, original.GetId(), decoded.GetId())
	assert.Equal(t, original.GetName(), decoded.GetName())
	assert.Equal(t, original.GetStatus(), decoded.GetStatus())
	assert.Equal(t, original.GetTags(), decoded.GetTags())
	assert.Equal(t, original.GetCreatedAt().AsTime(), decoded.GetCreatedAt().AsTime())
}

// TestSchemaRegistry_OrderRoundTrip tests the Order schema which has cross-file
// dependencies (Product, ProductStatus from product.proto).
func TestSchemaRegistry_OrderRoundTrip(t *testing.T) {
	srClient := startRedpanda(t)

	serde, err := commonkvstore.Proto(
		func() *examplev1.Order { return &examplev1.Order{} },
		commonkvstore.WithSchemaRegistry(srClient, examplev1.OrderSRSubject, examplev1.OrderSRSchema),
	)
	require.NoError(t, err)

	original := &examplev1.Order{
		Id: "order-1",
		Items: []*examplev1.OrderItem{
			{
				Product: &examplev1.Product{
					Id:   "prod-1",
					Name: "Widget",
				},
				Quantity: 3,
			},
		},
		Status:    examplev1.OrderStatus_ORDER_STATUS_PENDING,
		CreatedAt: timestamppb.Now(),
	}

	data, err := serde.Serialize(original)
	require.NoError(t, err)
	assert.Equal(t, byte(0x00), data[0])

	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, original.GetId(), decoded.GetId())
	assert.Equal(t, original.GetStatus(), decoded.GetStatus())
	require.Len(t, decoded.GetItems(), 1)
	assert.Equal(t, "prod-1", decoded.GetItems()[0].GetProduct().GetId())
	assert.Equal(t, int32(3), decoded.GetItems()[0].GetQuantity())
}

// TestSchemaRegistry_EventRoundTrip tests the Event schema, whose ProviderType
// enum dependency lives in a different proto package (example.other.v1).
// Registration fails against a real Schema Registry if the inlined enum is
// referenced by a dangling, partially package-qualified name.
func TestSchemaRegistry_EventRoundTrip(t *testing.T) {
	srClient := startRedpanda(t)

	serde, err := commonkvstore.Proto(
		func() *examplev1.Event { return &examplev1.Event{} },
		commonkvstore.WithSchemaRegistry(srClient, examplev1.EventSRSubject, examplev1.EventSRSchema),
	)
	require.NoError(t, err)

	original := &examplev1.Event{
		Id:       "evt-1",
		Provider: otherv1.ProviderType_PROVIDER_TYPE_ANTHROPIC,
	}

	data, err := serde.Serialize(original)
	require.NoError(t, err)
	assert.Equal(t, byte(0x00), data[0])

	decoded, err := serde.Deserialize(data)
	require.NoError(t, err)
	assert.Equal(t, original.GetId(), decoded.GetId())
	assert.Equal(t, original.GetProvider(), decoded.GetProvider())
}

// TestSchemaRegistry_BothSchemasCoexist registers both Product and Order schemas
// on the same SR instance and verifies they don't interfere with each other.
func TestSchemaRegistry_BothSchemasCoexist(t *testing.T) {
	srClient := startRedpanda(t)

	productSerde, err := commonkvstore.Proto(
		func() *examplev1.Product { return &examplev1.Product{} },
		commonkvstore.WithSchemaRegistry(srClient, examplev1.ProductSRSubject, examplev1.ProductSRSchema),
	)
	require.NoError(t, err)

	orderSerde, err := commonkvstore.Proto(
		func() *examplev1.Order { return &examplev1.Order{} },
		commonkvstore.WithSchemaRegistry(srClient, examplev1.OrderSRSubject, examplev1.OrderSRSchema),
	)
	require.NoError(t, err)

	// Serialize both.
	productData, err := productSerde.Serialize(&examplev1.Product{Id: "p1", Name: "A"})
	require.NoError(t, err)

	orderData, err := orderSerde.Serialize(&examplev1.Order{Id: "o1", Status: examplev1.OrderStatus_ORDER_STATUS_SHIPPED})
	require.NoError(t, err)

	// Deserialize each with the correct serde.
	p, err := productSerde.Deserialize(productData)
	require.NoError(t, err)
	assert.Equal(t, "p1", p.GetId())

	o, err := orderSerde.Deserialize(orderData)
	require.NoError(t, err)
	assert.Equal(t, "o1", o.GetId())
	assert.Equal(t, examplev1.OrderStatus_ORDER_STATUS_SHIPPED, o.GetStatus())
}

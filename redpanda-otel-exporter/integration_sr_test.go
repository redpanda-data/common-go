package redpandaotelexporter

import (
	"context"
	"testing"
	"time"

	"github.com/bufbuild/protocompile"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/dynamicpb"

	pb "github.com/redpanda-data/common-go/redpanda-otel-exporter/proto"
)

// TestTraceExporter_SchemaRegistryProtobuf_EndToEnd tests complete flow:
// 1. Export traces to Redpanda using Schema Registry protobuf format
// 2. Consume with franz-go using Schema Registry deserialization
// 3. Validate protobuf content and SR encoding
func TestTraceExporter_SchemaRegistryProtobuf_EndToEnd(t *testing.T) {
	ctx := t.Context()
	brokers, schemaRegistryURL := setupRedpandaWithSchemaRegistry(t)

	topicName := "test-traces-sr-proto-e2e"
	res := createTestResource(t)

	// Create exporter with Schema Registry Protobuf format
	exporter, err := NewTraceExporter(
		WithTopic(topicName),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatSchemaRegistryProtobuf),
		WithSchemaRegistryURL(schemaRegistryURL),
	)
	require.NoError(t, err, "failed to create trace exporter")
	defer exporter.Shutdown(context.Background())

	// Create a test span with various attributes
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer tracerProvider.Shutdown(context.Background())

	tracer := tracerProvider.Tracer("test-tracer-e2e")
	_, span := tracer.Start(ctx, "test-sr-proto-e2e-operation",
		trace.WithAttributes(
			attribute.String("test.key", "test.value"),
			attribute.Int("test.count", 42),
			attribute.Bool("test.enabled", true),
		),
	)
	span.AddEvent("test-event", trace.WithAttributes(
		attribute.String("event.detail", "important"),
	))
	span.End()

	// Force flush
	err = tracerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush tracer provider")

	// Create consumer with franz-go
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers),
		kgo.ConsumeTopics(topicName),
	)
	require.NoError(t, err, "failed to create kafka consumer client")
	defer client.Close()

	// Poll for records with retries
	var records []*kgo.Record
	maxAttempts := 5
	pollTimeout := 1 * time.Second

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
		fetches := client.PollFetches(pollCtx)
		cancel()

		require.Empty(t, fetches.Errors(), "fetch errors occurred")

		fetches.EachRecord(func(r *kgo.Record) {
			if r.Topic == topicName {
				records = append(records, r)
			}
		})

		if len(records) > 0 {
			break
		}

		if attempt < maxAttempts {
			time.Sleep(200 * time.Millisecond)
		}
	}

	require.NotEmpty(t, records, "no message found in topic")
	record := records[0]

	t.Logf("Received record: topic=%s, partition=%d, offset=%d, key=%s, value_len=%d",
		record.Topic, record.Partition, record.Offset, string(record.Key), len(record.Value))

	// Verify Schema Registry wire format
	value := record.Value
	require.GreaterOrEqual(t, len(value), 6, "message too short for Schema Registry protobuf format")

	// Decode using ConfluentHeader
	var header sr.ConfluentHeader

	// Decode schema ID
	schemaID, remaining, err := header.DecodeID(value)
	require.NoError(t, err, "failed to decode Schema Registry schema ID")
	assert.Greater(t, schemaID, 0, "schema ID should be positive")
	t.Logf("Schema ID: %d", schemaID)

	// Decode protobuf message indexes
	messageIndexes, payload, err := header.DecodeIndex(remaining, 10)
	require.NoError(t, err, "failed to decode protobuf message indexes")
	require.Len(t, messageIndexes, 1, "expected single message index for top-level message")
	assert.Equal(t, 0, messageIndexes[0], "message index should be 0 for top-level message")
	t.Logf("Message indexes: %v", messageIndexes)

	// Unmarshal using the actual compiled proto (what we encode with)
	var spanActual pb.Span
	err = proto.Unmarshal(payload, &spanActual)
	require.NoError(t, err, "failed to unmarshal with compiled proto")

	// Unmarshal using dynamic proto from otlpTraceProtoSchema (what consumers see from SR)
	spanDynamic := unmarshalWithSchema(ctx, t, payload)

	// Convert dynamic proto to the same type for comparison
	// This validates that the schema in schemas.go matches the actual wire format
	spanFromDynamic := &pb.Span{}
	dynamicBytes, err := proto.Marshal(spanDynamic)
	require.NoError(t, err, "failed to marshal dynamic proto")
	err = proto.Unmarshal(dynamicBytes, spanFromDynamic)
	require.NoError(t, err, "failed to unmarshal dynamic bytes to pb.Span")

	// Compare the two spans - they should be identical
	// This proves that otlpTraceProtoSchema correctly represents the wire format
	assertProtoEqual(t, &spanActual, spanFromDynamic)

	// Basic validation that the span has expected content
	assert.Equal(t, "test-sr-proto-e2e-operation", spanActual.Name, "span name mismatch")
	assert.NotEmpty(t, spanActual.TraceId, "trace ID should not be empty")
	assert.NotEmpty(t, spanActual.SpanId, "span ID should not be empty")
	assert.Greater(t, spanActual.StartTimeUnixNano, uint64(0), "start time should be set")
	assert.Greater(t, spanActual.EndTimeUnixNano, spanActual.StartTimeUnixNano, "end time should be after start time")
	assert.NotEmpty(t, spanActual.Attributes, "attributes should not be empty")
	assert.Len(t, spanActual.Events, 1, "expected one event")

	// Verify resource attributes are embedded in the protobuf message
	require.NotNil(t, spanActual.Resource, "resource should be embedded in span")
	require.NotEmpty(t, spanActual.Resource.Attributes, "resource attributes should not be empty")

	// Find specific resource attributes
	resourceAttrs := make(map[string]string)
	for _, attr := range spanActual.Resource.Attributes {
		if attr.Value.GetStringValue() != "" {
			resourceAttrs[attr.Key] = attr.Value.GetStringValue()
		}
	}
	assert.Equal(t, "test-service", resourceAttrs["service.name"], "service.name attribute mismatch")
	assert.Equal(t, "1.0.0", resourceAttrs["service.version"], "service.version attribute mismatch")
	assert.Equal(t, "test", resourceAttrs["environment"], "environment attribute mismatch")

	// Verify scope is also embedded
	require.NotNil(t, spanActual.Scope, "scope should be embedded in span")

	t.Log("Successfully validated end-to-end Schema Registry protobuf encoding:")
	t.Logf("  - Schema ID: %d", schemaID)
	t.Logf("  - Message indexes: %v", messageIndexes)
	t.Logf("  - Span: %s", spanActual.Name)
	t.Logf("  - Attributes: %d", len(spanActual.Attributes))
	t.Logf("  - Events: %d", len(spanActual.Events))
	t.Logf("  - Resource attributes: %d", len(spanActual.Resource.Attributes))
	t.Logf("  - Scope: %s", spanActual.Scope.Name)
	t.Log("  - Schema wire format validated: compiled proto == dynamic proto from schema")
}

// TestTraceExporter_SchemaRegistryProtobuf_MultipleSpans tests handling multiple spans
func TestTraceExporter_SchemaRegistryProtobuf_MultipleSpans(t *testing.T) {
	ctx := t.Context()
	brokers, schemaRegistryURL := setupRedpandaWithSchemaRegistry(t)

	topicName := "test-traces-sr-proto-multi"
	res := createTestResource(t)

	// Create exporter
	exporter, err := NewTraceExporter(
		WithTopic(topicName),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatSchemaRegistryProtobuf),
		WithSchemaRegistryURL(schemaRegistryURL),
	)
	require.NoError(t, err, "failed to create trace exporter")
	defer exporter.Shutdown(context.Background())

	// Create tracer provider
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer tracerProvider.Shutdown(context.Background())

	tracer := tracerProvider.Tracer("test-tracer-multi")

	// Create multiple spans
	spanCount := 5
	for i := range spanCount {
		_, span := tracer.Start(ctx, "multi-span-test",
			trace.WithAttributes(
				attribute.Int("span.index", i),
			),
		)
		span.End()
	}

	// Force flush
	err = tracerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush tracer provider")

	// Create consumer
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers),
		kgo.ConsumeTopics(topicName),
	)
	require.NoError(t, err, "failed to create kafka consumer client")
	defer client.Close()

	// Poll for all records
	var records []*kgo.Record
	maxAttempts := 10
	pollTimeout := 1 * time.Second

	for attempt := 1; attempt <= maxAttempts && len(records) < spanCount; attempt++ {
		pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
		fetches := client.PollFetches(pollCtx)
		cancel()

		require.Empty(t, fetches.Errors(), "fetch errors occurred")

		fetches.EachRecord(func(r *kgo.Record) {
			if r.Topic == topicName {
				records = append(records, r)
			}
		})

		if attempt < maxAttempts && len(records) < spanCount {
			time.Sleep(200 * time.Millisecond)
		}
	}

	require.Len(t, records, spanCount, "expected %d spans", spanCount)

	// Validate all spans
	var header sr.ConfluentHeader
	spanIndices := make(map[int]bool)

	for idx, record := range records {
		// Decode SR header
		schemaID, remaining, err := header.DecodeID(record.Value)
		require.NoError(t, err, "failed to decode schema ID for record %d", idx)
		assert.Greater(t, schemaID, 0, "schema ID should be positive")

		messageIndexes, payload, err := header.DecodeIndex(remaining, 10)
		require.NoError(t, err, "failed to decode message indexes for record %d", idx)
		require.Len(t, messageIndexes, 1, "expected single message index")
		assert.Equal(t, 0, messageIndexes[0], "message index should be 0")

		// Unmarshal protobuf
		var spanProto pb.Span
		err = proto.Unmarshal(payload, &spanProto)
		require.NoError(t, err, "failed to unmarshal span %d", idx)

		assert.Equal(t, "multi-span-test", spanProto.Name)

		// Extract span index from attributes
		var spanIndex int64 = -1
		for _, attr := range spanProto.Attributes {
			if attr.Key == "span.index" {
				spanIndex = attr.Value.GetIntValue()
				break
			}
		}
		require.GreaterOrEqual(t, spanIndex, int64(0), "span.index not found")
		spanIndices[int(spanIndex)] = true
	}

	// Verify we got all indices
	assert.Len(t, spanIndices, spanCount, "should have all unique span indices")
	for i := range spanCount {
		assert.True(t, spanIndices[i], "missing span index %d", i)
	}

	t.Logf("Successfully validated %d spans with Schema Registry protobuf encoding", spanCount)
}

// unmarshalWithSchema unmarshals protobuf data using the schema from otlpTraceProtoSchema
// This simulates what Schema Registry consumers do when decoding messages
func unmarshalWithSchema(ctx context.Context, t *testing.T, payload []byte) proto.Message {
	t.Helper()

	// Compile the schema string into file descriptors
	// Include both trace.proto and common.proto since trace.proto imports common.proto
	compiler := protocompile.Compiler{
		Resolver: &protocompile.SourceResolver{
			Accessor: protocompile.SourceAccessorFromMap(map[string]string{
				"trace.proto":  otlpTraceProtoSchema,
				"common.proto": otlpCommonProtoSchema,
			}),
		},
	}

	fds, err := compiler.Compile(ctx, "trace.proto")
	require.NoError(t, err, "failed to compile otlpTraceProtoSchema")
	require.Len(t, fds, 1, "expected one compiled file")

	// Get the Span message descriptor (index 0)
	spanDesc := fds[0].Messages().Get(0)
	require.Equal(t, "Span", string(spanDesc.Name()), "first message should be Span")

	// Create dynamic message and unmarshal
	spanDynamic := dynamicpb.NewMessage(spanDesc)
	err = proto.Unmarshal(payload, spanDynamic)
	require.NoError(t, err, "failed to unmarshal with dynamic proto from schema")

	return spanDynamic
}

// assertProtoEqual compares two protos using protocmp
func assertProtoEqual(t *testing.T, want, got proto.Message) {
	t.Helper()
	if proto.Equal(want, got) {
		return
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Fatalf("Protos not equal (-want +got):\n%s\n\nThis means otlpTraceProtoSchema doesn't match the actual OTLP proto wire format", diff)
	}
}

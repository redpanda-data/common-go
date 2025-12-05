package redpandaotelexporter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
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
		WithClientID("test-trace-exporter-sr-proto-e2e"),
		WithResource(res),
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

	// Unmarshal the protobuf payload
	var spanProto tracepb.Span
	err = proto.Unmarshal(payload, &spanProto)
	require.NoError(t, err, "failed to unmarshal protobuf payload")

	// Validate span content
	assert.Equal(t, "test-sr-proto-e2e-operation", spanProto.Name, "span name mismatch")
	assert.NotEmpty(t, spanProto.TraceId, "trace ID should not be empty")
	assert.NotEmpty(t, spanProto.SpanId, "span ID should not be empty")
	assert.Greater(t, spanProto.StartTimeUnixNano, uint64(0), "start time should be set")
	assert.Greater(t, spanProto.EndTimeUnixNano, spanProto.StartTimeUnixNano, "end time should be after start time")

	// Validate attributes
	require.NotEmpty(t, spanProto.Attributes, "attributes should not be empty")
	attrMap := make(map[string]*commonpb.AnyValue)
	for _, attr := range spanProto.Attributes {
		attrMap[attr.Key] = attr.Value
	}

	// Check string attribute
	require.Contains(t, attrMap, "test.key")
	require.NotNil(t, attrMap["test.key"].GetStringValue())
	assert.Equal(t, "test.value", attrMap["test.key"].GetStringValue())

	// Check int attribute
	require.Contains(t, attrMap, "test.count")
	assert.Equal(t, int64(42), attrMap["test.count"].GetIntValue())

	// Check bool attribute
	require.Contains(t, attrMap, "test.enabled")
	assert.Equal(t, true, attrMap["test.enabled"].GetBoolValue())

	// Validate events
	require.Len(t, spanProto.Events, 1, "expected one event")
	event := spanProto.Events[0]
	assert.Equal(t, "test-event", event.Name)
	require.NotEmpty(t, event.Attributes, "event should have attributes")

	eventAttrMap := make(map[string]*commonpb.AnyValue)
	for _, attr := range event.Attributes {
		eventAttrMap[attr.Key] = attr.Value
	}
	require.Contains(t, eventAttrMap, "event.detail")
	assert.Equal(t, "important", eventAttrMap["event.detail"].GetStringValue())

	// Validate status
	assert.NotNil(t, spanProto.Status)
	// Status should be OK (1) or UNSET (0) for successful span
	assert.Contains(t, []tracepb.Status_StatusCode{
		tracepb.Status_STATUS_CODE_UNSET,
		tracepb.Status_STATUS_CODE_OK,
	}, spanProto.Status.Code)

	// Verify resource attributes in headers
	require.NotEmpty(t, record.Headers, "headers should not be empty")
	headerMap := make(map[string]string)
	for _, h := range record.Headers {
		headerMap[h.Key] = string(h.Value)
	}
	assert.Equal(t, "test-service", headerMap["service.name"], "service.name header mismatch")
	assert.Equal(t, "1.0.0", headerMap["service.version"], "service.version header mismatch")
	assert.Equal(t, "test", headerMap["environment"], "environment header mismatch")

	t.Logf("Successfully validated end-to-end Schema Registry protobuf encoding:")
	t.Logf("  - Schema ID: %d", schemaID)
	t.Logf("  - Message indexes: %v", messageIndexes)
	t.Logf("  - Span: %s", spanProto.Name)
	t.Logf("  - Attributes: %d", len(spanProto.Attributes))
	t.Logf("  - Events: %d", len(spanProto.Events))
	t.Logf("  - Resource headers: %d", len(record.Headers))
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
		WithClientID("test-trace-exporter-sr-proto-multi"),
		WithResource(res),
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
	for i := 0; i < spanCount; i++ {
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
		var spanProto tracepb.Span
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
	for i := 0; i < spanCount; i++ {
		assert.True(t, spanIndices[i], "missing span index %d", i)
	}

	t.Logf("Successfully validated %d spans with Schema Registry protobuf encoding", spanCount)
}

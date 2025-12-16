package redpandaotelexporter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	pb "buf.build/gen/go/redpandadata/otel/protocolbuffers/go/redpanda/otel/v1"
)

// useLocalBroker allows using a local Redpanda instance at localhost:9092 instead of testcontainers
// Usage: go test -local-broker
var useLocalBroker = flag.Bool("local-broker", false, "use local Redpanda at localhost:9092 instead of testcontainers")

// setupRedpanda starts a Redpanda container for testing or uses a local instance
func setupRedpanda(t *testing.T) string {
	t.Helper()

	// Check if using local Redpanda
	if *useLocalBroker {
		t.Log("Using local Redpanda at localhost:9092")
		return "localhost:9092"
	}

	// Use testcontainers
	ctx := t.Context()
	container, err := redpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:latest")
	require.NoError(t, err, "failed to start redpanda container")

	// Register cleanup
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	})

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err, "failed to get kafka seed broker")

	return brokers
}

// createTestResource creates a test resource with standard attributes
func createTestResource(t *testing.T) *resource.Resource {
	t.Helper()

	res, err := resource.New(t.Context(),
		resource.WithAttributes(
			semconv.ServiceName("test-service"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "test"),
		),
	)
	require.NoError(t, err, "failed to create resource")

	return res
}

// consumeRecords reads records from a Kafka topic
func consumeRecords(t *testing.T, brokers, topic string) []*kgo.Record {
	t.Helper()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers),
		kgo.ConsumeTopics(topic),
	)
	require.NoError(t, err, "failed to create kafka consumer client")
	defer client.Close()

	// Retry up to 3 times with short timeout per attempt
	maxAttempts := 3
	pollTimeout := 500 * time.Millisecond

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(t.Context(), pollTimeout)
		fetches := client.PollFetches(ctx)
		cancel()

		require.Empty(t, fetches.Errors(), "fetch errors occurred")

		var records []*kgo.Record
		fetches.EachRecord(func(r *kgo.Record) {
			if r.Topic == topic {
				records = append(records, r)
			}
		})

		if len(records) > 0 {
			return records
		}

		// If not the last attempt, wait a bit before retrying
		if attempt < maxAttempts {
			time.Sleep(100 * time.Millisecond)
		}
	}

	return nil
}

// assertHexEncoded validates that a string is properly hex-encoded using regex
// assertBase64Encoded validates that a value is a valid base64 string with expected decoded byte length
func assertBase64Encoded(t *testing.T, value string, expectedByteLen int, fieldName string) {
	t.Helper()

	// Decode base64
	decoded, err := base64.StdEncoding.DecodeString(value)
	require.NoError(t, err, "%s should be valid base64", fieldName)
	assert.Len(t, decoded, expectedByteLen, "%s decoded length mismatch", fieldName)
}

// TestTraceExporter_JSON tests trace exporter with JSON serialization
func TestTraceExporter_JSON(t *testing.T) {
	ctx := t.Context()
	brokers := setupRedpanda(t)

	res := createTestResource(t)

	// Create exporter
	exporter, err := NewTraceExporter(
		WithTopic("test-traces-json"),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatJSON),
	)
	require.NoError(t, err, "failed to create trace exporter")
	defer exporter.Shutdown(context.Background())

	// Create a test span
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer tracerProvider.Shutdown(context.Background())

	tracer := tracerProvider.Tracer("test-tracer")
	_, span := tracer.Start(ctx, "test-operation",
		trace.WithAttributes(
			attribute.String("test.key", "test.value"),
			attribute.Int("test.count", 42),
		),
	)
	span.AddEvent("test-event")
	span.SetStatus(codes.Ok, "it works!")
	span.End()

	// Force flush
	err = tracerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush tracer provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, "test-traces-json")
	require.NotEmpty(t, records, "no message found in topic")

	// Parse and validate the span data
	var spanData map[string]any
	err = json.Unmarshal(records[0].Value, &spanData)
	require.NoError(t, err, "failed to unmarshal span data")

	// Verify basic fields
	assert.Equal(t, "test-operation", spanData["name"], "span name mismatch")

	// Verify traceId is base64 encoded (16 bytes) - protojson encodes bytes as base64
	traceID, ok := spanData["trace_id"].(string)
	require.True(t, ok, "traceId not found or not a string")
	assertBase64Encoded(t, traceID, 16, "trace_id")

	// Verify spanId is base64 encoded (8 bytes) - protojson encodes bytes as base64
	spanID, ok := spanData["span_id"].(string)
	require.True(t, ok, "spanId not found or not a string")
	assertBase64Encoded(t, spanID, 8, "span_id")

	// Verify kind - OTLP JSON spec requires numeric values for enums
	kind, ok := spanData["kind"].(float64)
	require.True(t, ok, "kind not found or not a number (got type %T), OTLP spec requires integer enum", spanData["kind"])
	// SpanKind values: UNSPECIFIED=0, INTERNAL=1, SERVER=2, CLIENT=3, PRODUCER=4, CONSUMER=5
	assert.GreaterOrEqual(t, kind, 0.0, "kind value out of range")
	assert.LessOrEqual(t, kind, 5.0, "kind value out of range")

	// Verify status structure and code - OTLP JSON spec requires numeric values for enums
	status, ok := spanData["status"].(map[string]any)
	require.True(t, ok, "status not found or wrong type")

	statusCode, ok := status["code"].(float64)
	require.True(t, ok, "status.code not found or not a number (got type %T), OTLP spec requires integer enum", status["code"])
	assert.Equal(t, 1.0, statusCode, "status.code should be OK (1)")

	// Verify attributes - OTLP JSON spec requires array of {key, value} objects
	// where value is an AnyValue with typed fields like stringValue, intValue, etc.
	attrs, ok := spanData["attributes"].([]any)
	require.True(t, ok, "attributes not found or wrong type")
	require.GreaterOrEqual(t, len(attrs), 2, "expected at least 2 attributes")

	// Convert array to map for easier validation
	attrMap := make(map[string]map[string]any)
	for _, attr := range attrs {
		attrObj, ok := attr.(map[string]any)
		require.True(t, ok, "attribute is not an object")
		key, ok := attrObj["key"].(string)
		require.True(t, ok, "attribute key is not a string")
		value, ok := attrObj["value"].(map[string]any)
		require.True(t, ok, "attribute value is not an AnyValue object")
		attrMap[key] = value
	}

	// Verify string attribute
	stringValue, ok := attrMap["test.key"]["string_value"].(string)
	require.True(t, ok, "test.key stringValue not found")
	assert.Equal(t, "test.value", stringValue, "string attribute mismatch")

	// Verify int attribute (stored as string per OTLP spec)
	intValue, ok := attrMap["test.count"]["int_value"].(string)
	require.True(t, ok, "test.count intValue not found")
	assert.Equal(t, "42", intValue, "numeric attribute mismatch")
}

// TestTraceExporter_Protobuf tests trace exporter with Protobuf serialization
func TestTraceExporter_Protobuf(t *testing.T) {
	ctx := t.Context()
	brokers := setupRedpanda(t)

	res := createTestResource(t)

	// Create exporter
	exporter, err := NewTraceExporter(
		WithTopic("test-traces-protobuf"),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatProtobuf),
	)
	require.NoError(t, err, "failed to create trace exporter")
	defer exporter.Shutdown(context.Background())

	// Create a test span
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer tracerProvider.Shutdown(context.Background())

	tracer := tracerProvider.Tracer("test-tracer")
	_, span := tracer.Start(ctx, "test-protobuf-operation",
		trace.WithAttributes(
			attribute.String("format", "protobuf"),
		),
	)
	span.End()

	// Force flush
	err = tracerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush tracer provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, "test-traces-protobuf")
	require.NotEmpty(t, records, "no message found in topic")

	// Parse protobuf
	var protoSpan pb.Span
	err = proto.Unmarshal(records[0].Value, &protoSpan)
	require.NoError(t, err, "failed to unmarshal protobuf")

	// Verify span
	assert.Equal(t, "test-protobuf-operation", protoSpan.Name, "span name mismatch")

	// Verify resource attributes are embedded in the protobuf message
	require.NotNil(t, protoSpan.Resource, "resource should be embedded in span")
	require.NotEmpty(t, protoSpan.Resource.Attributes, "resource attributes should not be empty")

	// Find service.name in resource attributes
	resourceAttrs := make(map[string]string)
	for _, attr := range protoSpan.Resource.Attributes {
		if attr.Value.GetStringValue() != "" {
			resourceAttrs[attr.Key] = attr.Value.GetStringValue()
		}
	}
	assert.Equal(t, "test-service", resourceAttrs["service.name"], "service.name attribute mismatch")

	// Verify scope is also embedded
	require.NotNil(t, protoSpan.Scope, "scope should be embedded in span")
}

// TestMetricExporter tests metric exporter
func TestMetricExporter(t *testing.T) {
	ctx := t.Context()
	brokers := setupRedpanda(t)

	res := createTestResource(t)

	// Create exporter
	exporter, err := NewMetricExporter(
		WithTopic("test-metrics"),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatJSON),
	)
	require.NoError(t, err, "failed to create metric exporter")
	defer exporter.Shutdown(context.Background())

	// Create meter provider
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(1*time.Second))),
		sdkmetric.WithResource(res),
	)
	defer meterProvider.Shutdown(context.Background())

	// Create and record metrics
	meter := meterProvider.Meter("test-meter")
	counter, err := meter.Int64Counter("test.counter")
	require.NoError(t, err, "failed to create counter")

	counter.Add(ctx, 10, metric.WithAttributes(
		attribute.String("test", "value"),
	))

	// Force flush
	err = meterProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush meter provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, "test-metrics")
	require.NotEmpty(t, records, "no metric message found")

	t.Logf("Found metric message of size %d bytes", len(records[0].Value))
}

// TestMetricExporter_Protobuf tests metric exporter with protobuf format
func TestMetricExporter_Protobuf(t *testing.T) {
	ctx := t.Context()
	brokers := setupRedpanda(t)

	res := createTestResource(t)

	// Create exporter with protobuf format
	exporter, err := NewMetricExporter(
		WithTopic("test-metrics-protobuf"),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatProtobuf),
	)
	require.NoError(t, err, "failed to create metric exporter")
	defer exporter.Shutdown(context.Background())

	// Create meter provider
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter,
			sdkmetric.WithInterval(1*time.Second))),
		sdkmetric.WithResource(res),
	)
	defer meterProvider.Shutdown(context.Background())

	// Create and record metrics
	meter := meterProvider.Meter("test-meter-pb")
	counter, err := meter.Int64Counter("test.counter.pb")
	require.NoError(t, err, "failed to create counter")

	counter.Add(ctx, 15, metric.WithAttributes(
		attribute.String("format", "protobuf"),
	))

	// Force flush
	err = meterProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush meter provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, "test-metrics-protobuf")
	require.NotEmpty(t, records, "no metric message found")

	t.Logf("Found protobuf metric message of size %d bytes", len(records[0].Value))

	// Parse protobuf
	var protoMetric pb.Metric
	err = proto.Unmarshal(records[0].Value, &protoMetric)
	require.NoError(t, err, "failed to unmarshal protobuf")

	// Verify metric
	assert.Equal(t, "test.counter.pb", protoMetric.Name, "metric name mismatch")

	// Verify resource attributes are embedded in the protobuf message
	require.NotNil(t, protoMetric.Resource, "resource should be embedded in metric")
	require.NotEmpty(t, protoMetric.Resource.Attributes, "resource attributes should not be empty")

	// Find service.name in resource attributes
	resourceAttrs := make(map[string]string)
	for _, attr := range protoMetric.Resource.Attributes {
		if attr.Value.GetStringValue() != "" {
			resourceAttrs[attr.Key] = attr.Value.GetStringValue()
		}
	}
	assert.Equal(t, "test-service", resourceAttrs["service.name"], "service.name attribute mismatch")

	// Verify scope is also embedded
	require.NotNil(t, protoMetric.Scope, "scope should be embedded in metric")
}

// TestLogExporter tests log exporter
func TestLogExporter(t *testing.T) {
	ctx := t.Context()
	brokers := setupRedpanda(t)

	res := createTestResource(t)

	// Create exporter
	exporter, err := NewLogExporter(
		WithTopic("test-logs"),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatJSON),
	)
	require.NoError(t, err, "failed to create log exporter")
	defer exporter.Shutdown(context.Background())

	// Create logger provider
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)
	defer loggerProvider.Shutdown(context.Background())

	// Emit a log
	logger := loggerProvider.Logger("test-logger")
	logRecord := otellog.Record{}
	logRecord.SetTimestamp(time.Now())
	logRecord.SetBody(otellog.StringValue("test log message"))
	logRecord.SetSeverity(otellog.SeverityInfo)
	logRecord.AddAttributes(
		otellog.String("test.attribute", "test.value"),
	)
	logger.Emit(ctx, logRecord)

	// Force flush
	err = loggerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush logger provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, "test-logs")
	require.NotEmpty(t, records, "no log message found")

	// Parse and validate log data
	var logData map[string]any
	err = json.Unmarshal(records[0].Value, &logData)
	require.NoError(t, err, "failed to unmarshal log data")

	// Verify body - OTLP JSON spec requires AnyValue typed structure
	body, ok := logData["body"].(map[string]any)
	require.True(t, ok, "body is not an AnyValue object")
	bodyValue, ok := body["string_value"].(string)
	require.True(t, ok, "body stringValue not found")
	assert.Equal(t, "test log message", bodyValue, "log body mismatch")
}

// TestLogExporter_Protobuf tests log exporter with protobuf format
func TestLogExporter_Protobuf(t *testing.T) {
	ctx := t.Context()
	brokers := setupRedpanda(t)

	res := createTestResource(t)

	// Create exporter with protobuf format
	exporter, err := NewLogExporter(
		WithTopic("test-logs-protobuf"),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatProtobuf),
	)
	require.NoError(t, err, "failed to create log exporter")
	defer exporter.Shutdown(context.Background())

	// Create logger provider
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
		sdklog.WithResource(res),
	)
	defer loggerProvider.Shutdown(context.Background())

	// Emit a log
	logger := loggerProvider.Logger("test-logger-pb")
	logRecord := otellog.Record{}
	logRecord.SetTimestamp(time.Now())
	logRecord.SetBody(otellog.StringValue("test protobuf log message"))
	logRecord.SetSeverity(otellog.SeverityWarn)
	logRecord.AddAttributes(
		otellog.String("format", "protobuf"),
	)
	logger.Emit(ctx, logRecord)

	// Force flush
	err = loggerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush logger provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, "test-logs-protobuf")
	require.NotEmpty(t, records, "no log message found")

	t.Logf("Found protobuf log message of size %d bytes", len(records[0].Value))

	// Parse protobuf
	var protoLog pb.LogRecord
	err = proto.Unmarshal(records[0].Value, &protoLog)
	require.NoError(t, err, "failed to unmarshal protobuf")

	// Verify log record
	assert.Equal(t, pb.SeverityNumber_SEVERITY_NUMBER_WARN, protoLog.SeverityNumber, "severity mismatch")

	// Verify resource attributes are embedded in the protobuf message
	require.NotNil(t, protoLog.Resource, "resource should be embedded in log")
	require.NotEmpty(t, protoLog.Resource.Attributes, "resource attributes should not be empty")

	// Find service.name in resource attributes
	resourceAttrs := make(map[string]string)
	for _, attr := range protoLog.Resource.Attributes {
		if attr.Value.GetStringValue() != "" {
			resourceAttrs[attr.Key] = attr.Value.GetStringValue()
		}
	}
	assert.Equal(t, "test-service", resourceAttrs["service.name"], "service.name attribute mismatch")

	// Verify scope is also embedded
	require.NotNil(t, protoLog.Scope, "scope should be embedded in log")
}

// setupRedpandaWithSchemaRegistry starts a Redpanda container with Schema Registry enabled
func setupRedpandaWithSchemaRegistry(t *testing.T) (brokers string, sr string) {
	t.Helper()

	// Check if using local Redpanda
	if *useLocalBroker {
		t.Log("Using local Redpanda at localhost:9092 with Schema Registry at http://localhost:8081")
		return "localhost:9092", "http://localhost:8081"
	}

	// Use testcontainers
	ctx := t.Context()
	container, err := redpanda.Run(ctx, "docker.redpanda.com/redpandadata/redpanda:latest")
	require.NoError(t, err, "failed to start redpanda container")

	// Register cleanup
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	})

	brokers, err = container.KafkaSeedBroker(ctx)
	require.NoError(t, err, "failed to get kafka seed broker")

	schemaRegistryURL, err := container.SchemaRegistryAddress(ctx)
	require.NoError(t, err, "failed to get schema registry address")

	return brokers, schemaRegistryURL
}

// TestTraceExporter_SchemaRegistryJSON tests trace exporter with Schema Registry JSON serialization
func TestTraceExporter_SchemaRegistryJSON(t *testing.T) {
	ctx := t.Context()
	brokers, schemaRegistryURL := setupRedpandaWithSchemaRegistry(t)

	// Create topic
	topicName := "test-traces-sr-json"

	res := createTestResource(t)

	// Create exporter with Schema Registry format
	// The exporter will automatically register the built-in OTLP JSON schema
	exporter, err := NewTraceExporter(
		WithTopic(topicName),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatSchemaRegistryJSON),
		WithSchemaRegistryURL(schemaRegistryURL),
	)
	require.NoError(t, err, "failed to create trace exporter")
	defer exporter.Shutdown(context.Background())

	// Create a test span
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer tracerProvider.Shutdown(context.Background())

	tracer := tracerProvider.Tracer("test-tracer")
	_, span := tracer.Start(ctx, "test-sr-operation",
		trace.WithAttributes(
			attribute.String("test.key", "test.value"),
		),
	)
	span.End()

	// Force flush
	err = tracerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush tracer provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, topicName)
	require.NotEmpty(t, records, "no message found in topic")

	// Verify Schema Registry wire format using ConfluentHeader
	value := records[0].Value
	require.GreaterOrEqual(t, len(value), 5, "message too short for Schema Registry format")

	// Use ConfluentHeader to decode the wire format
	var header sr.ConfluentHeader
	schemaID, payload, err := header.DecodeID(value)
	require.NoError(t, err, "failed to decode Schema Registry header")
	assert.Greater(t, schemaID, 0, "schema ID should be positive")

	// Decode the JSON payload
	var spanData map[string]any
	err = json.Unmarshal(payload, &spanData)
	require.NoError(t, err, "failed to unmarshal JSON payload")

	// Verify basic fields
	assert.Equal(t, "test-sr-operation", spanData["name"], "span name mismatch")

	t.Logf("Successfully verified Schema Registry JSON format with schema ID %d", schemaID)
}

// TestTraceExporter_SchemaRegistryProtobuf tests trace exporter with Schema Registry Protobuf serialization
func TestTraceExporter_SchemaRegistryProtobuf(t *testing.T) {
	ctx := t.Context()
	brokers, schemaRegistryURL := setupRedpandaWithSchemaRegistry(t)

	// Create topic
	topicName := "test-traces-sr-protobuf"

	res := createTestResource(t)

	// Create exporter with Schema Registry Protobuf format
	// The exporter will automatically register the built-in OTLP protobuf schema
	exporter, err := NewTraceExporter(
		WithTopic(topicName),
		WithBrokers(brokers),
		WithSerializationFormat(SerializationFormatSchemaRegistryProtobuf),
		WithSchemaRegistryURL(schemaRegistryURL),
	)
	require.NoError(t, err, "failed to create trace exporter")
	defer exporter.Shutdown(context.Background())

	// Create a test span
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	defer tracerProvider.Shutdown(context.Background())

	tracer := tracerProvider.Tracer("test-tracer")
	_, span := tracer.Start(ctx, "test-sr-pb-operation",
		trace.WithAttributes(
			attribute.String("test.key", "test.value"),
		),
	)
	span.End()

	// Force flush
	err = tracerProvider.ForceFlush(ctx)
	require.NoError(t, err, "failed to flush tracer provider")

	// Consume records from Kafka
	records := consumeRecords(t, brokers, topicName)
	require.NotEmpty(t, records, "no message found in topic")

	// Verify Schema Registry wire format using ConfluentHeader
	value := records[0].Value
	require.GreaterOrEqual(t, len(value), 6, "message too short for Schema Registry protobuf format")

	// Use ConfluentHeader to decode the wire format
	var header sr.ConfluentHeader

	// First decode the schema ID
	schemaID, remaining, err := header.DecodeID(value)
	require.NoError(t, err, "failed to decode Schema Registry schema ID")
	assert.Greater(t, schemaID, 0, "schema ID should be positive")

	// Then decode the protobuf message indexes
	messageIndexes, payload, err := header.DecodeIndex(remaining, 10)
	require.NoError(t, err, "failed to decode protobuf message indexes")

	// For a top-level message, the message index array should contain a single 0
	require.Len(t, messageIndexes, 1, "expected single message index for top-level message")
	assert.Equal(t, 0, messageIndexes[0], "message index should be 0 for top-level message")

	// Decode the protobuf payload
	var spanProto pb.Span
	err = proto.Unmarshal(payload, &spanProto)
	require.NoError(t, err, "failed to unmarshal protobuf payload")

	// Verify basic fields
	assert.Equal(t, "test-sr-pb-operation", spanProto.Name, "span name mismatch")

	t.Logf("Successfully verified Schema Registry Protobuf format with schema ID %d and message indexes %v", schemaID, messageIndexes)
}

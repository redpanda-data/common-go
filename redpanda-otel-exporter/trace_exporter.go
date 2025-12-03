package redpandaotelexporter

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sr"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TraceExporter implements the OpenTelemetry trace exporter interface.
type TraceExporter struct {
	*Exporter
}

// Ensure TraceExporter implements the SpanExporter interface
var _ sdktrace.SpanExporter = (*TraceExporter)(nil)

// NewTraceExporter creates a new trace exporter that writes to Kafka.
// By default, spans are written to the "otel-traces" topic, which can be overridden using WithTopic.
//
// Required: WithBrokers option must be provided.
//
// Example:
//
//	exporter, err := NewTraceExporter(
//	    WithBrokers("localhost:9092"),
//	    WithResource(res),
//	    WithSerializationFormat(SerializationFormatJSON),
//	)
//
// To use a custom topic:
//
//	exporter, err := NewTraceExporter(
//	    WithBrokers("localhost:9092"),
//	    WithTopic("custom-traces"),
//	)
func NewTraceExporter(opts ...Option) (*TraceExporter, error) {
	exporter, err := newExporter("otel-traces", opts...)
	if err != nil {
		return nil, err
	}

	return &TraceExporter{
		Exporter: exporter,
	}, nil
}

// ExportSpans exports a batch of spans to Kafka.
func (e *TraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	switch e.config.serializationFormat {
	case SerializationFormatProtobuf:
		return e.exportSpansProtobuf(ctx, spans)
	case SerializationFormatSchemaRegistryJSON:
		return e.exportSpansSchemaRegistryJSON(ctx, spans)
	case SerializationFormatSchemaRegistryProtobuf:
		return e.exportSpansSchemaRegistryProtobuf(ctx, spans)
	default:
		return e.exportSpansJSON(ctx, spans)
	}
}

// exportSpansJSON exports spans in JSON format
func (e *TraceExporter) exportSpansJSON(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	// Register schema with Schema Registry if configured (for governance/documentation)
	if e.schemaRegistry != nil {
		subject := e.getSubjectName()
		schema := sr.Schema{
			Schema: otlpTraceJSONSchema,
			Type:   sr.TypeJSON,
		}
		// Ignore errors - schema registration is best-effort for plain format
		_, _ = e.registerSchema(ctx, subject, schema)
	}

	records := make([]*kgo.Record, 0, len(spans))

	for _, span := range spans {
		spanData := spanToMap(span)

		value, err := marshalJSON(spanData)
		if err != nil {
			return fmt.Errorf("failed to marshal span: %w", err)
		}

		// Use trace ID as the key for partitioning
		key := []byte(span.SpanContext().TraceID().String())

		record := &kgo.Record{
			Topic: e.config.topic,
			Key:   key,
			Value: value,
		}

		// Add resource attributes as Kafka headers instead of in the JSON body
		if e.config.resource != nil {
			record.Headers = resourceToHeaders(e.config.resource)
		}

		records = append(records, record)
	}

	return e.produceBatch(ctx, records)
}

// exportSpansProtobuf exports spans in OTLP protobuf format
// Each span is written as an individual record with resource attributes in headers
func (e *TraceExporter) exportSpansProtobuf(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}

	// Register schema with Schema Registry if configured (for governance/documentation)
	if e.schemaRegistry != nil {
		subject := e.getSubjectName()
		schema := sr.Schema{
			Schema: otlpTraceProtoSchema,
			Type:   sr.TypeProtobuf,
		}
		// Ignore errors - schema registration is best-effort for plain format
		_, _ = e.registerSchema(ctx, subject, schema)
	}

	records := make([]*kgo.Record, 0, len(spans))

	for _, span := range spans {
		// Convert span to protobuf
		spanProto := spanToProto(span)

		value, err := marshalProtobuf(spanProto)
		if err != nil {
			return fmt.Errorf("failed to marshal span to protobuf: %w", err)
		}

		// Use trace ID as the key for partitioning (same as JSON format)
		key := []byte(span.SpanContext().TraceID().String())

		record := &kgo.Record{
			Topic: e.config.topic,
			Key:   key,
			Value: value,
		}

		// Add resource attributes as Kafka headers (same as JSON format)
		if e.config.resource != nil {
			record.Headers = resourceToHeaders(e.config.resource)
		}

		records = append(records, record)
	}

	return e.produceBatch(ctx, records)
}

// exportSpansSchemaRegistryJSON exports spans in JSON format with Schema Registry serdes encoding
func (e *TraceExporter) exportSpansSchemaRegistryJSON(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	records := make([]*kgo.Record, 0, len(spans))

	// Get the subject name to use for schema registration
	subject := e.getSubjectName()

	for _, span := range spans {
		spanData := spanToMap(span)

		// Marshal to JSON
		jsonData, err := marshalJSON(spanData)
		if err != nil {
			return fmt.Errorf("failed to marshal span to JSON: %w", err)
		}

		// Encode with Schema Registry serdes format (JSON)
		schema := sr.Schema{
			Schema: otlpTraceJSONSchema,
			Type:   sr.TypeJSON,
		}
		value, err := e.encodeWithSchemaRegistry(ctx, jsonData, subject, schema)
		if err != nil {
			return fmt.Errorf("failed to encode span with schema registry: %w", err)
		}

		// Use trace ID as the key for partitioning
		key := []byte(span.SpanContext().TraceID().String())

		record := &kgo.Record{
			Topic: e.config.topic,
			Key:   key,
			Value: value,
		}

		// Add resource attributes as Kafka headers
		if e.config.resource != nil {
			record.Headers = resourceToHeaders(e.config.resource)
		}

		records = append(records, record)
	}

	return e.produceBatch(ctx, records)
}

// exportSpansSchemaRegistryProtobuf exports spans in OTLP protobuf format with Schema Registry serdes encoding
func (e *TraceExporter) exportSpansSchemaRegistryProtobuf(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	records := make([]*kgo.Record, 0, len(spans))

	// Get the subject name to use for schema registration
	subject := e.getSubjectName()

	for _, span := range spans {
		// Convert span to protobuf
		spanProto := spanToProto(span)

		// Marshal to protobuf
		protoData, err := marshalProtobuf(spanProto)
		if err != nil {
			return fmt.Errorf("failed to marshal span to protobuf: %w", err)
		}

		// Encode with Schema Registry serdes format (Protobuf with message indexes)
		schema := sr.Schema{
			Schema: otlpTraceProtoSchema,
			Type:   sr.TypeProtobuf,
		}
		value, err := e.encodeWithSchemaRegistry(ctx, protoData, subject, schema)
		if err != nil {
			return fmt.Errorf("failed to encode span protobuf with schema registry: %w", err)
		}

		// Use trace ID as the key for partitioning
		key := []byte(span.SpanContext().TraceID().String())

		record := &kgo.Record{
			Topic: e.config.topic,
			Key:   key,
			Value: value,
		}

		// Add resource attributes as Kafka headers
		if e.config.resource != nil {
			record.Headers = resourceToHeaders(e.config.resource)
		}

		records = append(records, record)
	}

	return e.produceBatch(ctx, records)
}

// Shutdown shuts down the exporter.
func (e *TraceExporter) Shutdown(ctx context.Context) error {
	return e.Exporter.Shutdown(ctx)
}

// spanToMap converts a ReadOnlySpan to a map for JSON serialization
// Following OTLP JSON encoding specification:
// - traceId and spanId: hex-encoded strings (not base64)
// - enums: integer values (not string names)
// - timestamps: string encoded uint64 nanoseconds (to avoid precision loss)
func spanToMap(span sdktrace.ReadOnlySpan) map[string]any {
	sc := span.SpanContext()

	result := map[string]any{
		"name":              span.Name(),
		"traceId":           sc.TraceID().String(), // Hex encoded per OTLP spec
		"spanId":            sc.SpanID().String(),  // Hex encoded per OTLP spec
		"kind":              int(span.SpanKind()),  // Integer enum per OTLP spec
		"startTimeUnixNano": fmt.Sprintf("%d", span.StartTime().UnixNano()),
		"endTimeUnixNano":   fmt.Sprintf("%d", span.EndTime().UnixNano()),
		"attributes":        attributesToArray(span.Attributes()),
	}

	// Add parent span ID if present
	if span.Parent().IsValid() {
		result["parentSpanId"] = span.Parent().SpanID().String() // Hex encoded
	}

	// Add status
	status := span.Status()
	result["status"] = map[string]any{
		"code":    int(status.Code), // Integer enum per OTLP spec
		"message": status.Description,
	}

	// Add events
	events := span.Events()
	if len(events) > 0 {
		eventMaps := make([]map[string]any, len(events))
		for i, event := range events {
			eventMaps[i] = map[string]any{
				"name":         event.Name,
				"timeUnixNano": fmt.Sprintf("%d", event.Time.UnixNano()),
				"attributes":   attributesToArray(event.Attributes),
			}
		}
		result["events"] = eventMaps
	}

	// Add links
	links := span.Links()
	if len(links) > 0 {
		linkMaps := make([]map[string]any, len(links))
		for i, link := range links {
			linkMaps[i] = map[string]any{
				"traceId":    link.SpanContext.TraceID().String(),
				"spanId":     link.SpanContext.SpanID().String(),
				"attributes": attributesToArray(link.Attributes),
			}
		}
		result["links"] = linkMaps
	}

	// Add trace state if present
	if sc.TraceState().Len() > 0 {
		result["traceState"] = sc.TraceState().String()
	}

	// Add trace flags as integer (not string) per OTLP spec
	result["flags"] = uint32(sc.TraceFlags())

	// Add instrumentation scope
	if scope := span.InstrumentationScope(); scope.Name != "" {
		scopeMap := map[string]any{
			"name": scope.Name,
		}
		if scope.Version != "" {
			scopeMap["version"] = scope.Version
		}
		if scope.SchemaURL != "" {
			scopeMap["schemaUrl"] = scope.SchemaURL
		}
		result["instrumentationScope"] = scopeMap
	}

	return result
}

// ForceFlush flushes any pending spans to Kafka.
func (e *TraceExporter) ForceFlush(ctx context.Context) error {
	return e.client.Flush(ctx)
}

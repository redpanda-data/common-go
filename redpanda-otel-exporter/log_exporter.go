package redpandaotelexporter

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// LogExporter implements the OpenTelemetry log exporter interface.
type LogExporter struct {
	*Exporter
}

// Ensure LogExporter implements the Exporter interface
var _ sdklog.Exporter = (*LogExporter)(nil)

// NewLogExporter creates a new log exporter that writes to Kafka.
// By default, logs are written to the "otel-logs" topic, which can be overridden using WithTopic.
//
// Required: WithBrokers option must be provided.
//
// Example:
//
//	exporter, err := NewLogExporter(
//	    WithBrokers("localhost:9092"),
//	    WithResource(res),
//	)
//
// To use a custom topic:
//
//	exporter, err := NewLogExporter(
//	    WithBrokers("localhost:9092"),
//	    WithTopic("custom-logs"),
//	)
func NewLogExporter(opts ...Option) (*LogExporter, error) {
	exporter, err := newExporter("otel-logs", opts...)
	if err != nil {
		return nil, err
	}

	return &LogExporter{
		Exporter: exporter,
	}, nil
}

// Export exports a batch of log records to Kafka.
func (e *LogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if len(records) == 0 {
		return nil
	}

	if e.config.serializationFormat == SerializationFormatProtobuf {
		return e.exportLogsProtobuf(ctx, records)
	}
	return e.exportLogsJSON(ctx, records)
}

// exportLogsJSON exports logs in JSON format
func (e *LogExporter) exportLogsJSON(ctx context.Context, records []sdklog.Record) error {
	kafkaRecords := make([]*kgo.Record, 0, len(records))

	for _, record := range records {
		logData := e.logRecordToMap(record)

		value, err := marshalJSON(logData)
		if err != nil {
			return fmt.Errorf("failed to marshal log record: %w", err)
		}

		kafkaRecord := &kgo.Record{
			Topic: e.config.topic,
			Key:   nil,
			Value: value,
		}

		// Add resource attributes as Kafka headers instead of in the JSON body
		if e.config.resource != nil {
			kafkaRecord.Headers = resourceToHeaders(e.config.resource)
		}

		kafkaRecords = append(kafkaRecords, kafkaRecord)
	}

	return e.produceBatch(ctx, kafkaRecords)
}

// exportLogsProtobuf exports logs in OTLP protobuf format
// Each log is written as an individual record with resource attributes in headers
func (e *LogExporter) exportLogsProtobuf(ctx context.Context, records []sdklog.Record) error {
	if len(records) == 0 {
		return nil
	}

	kafkaRecords := make([]*kgo.Record, 0, len(records))

	for _, record := range records {
		// Convert log record to protobuf
		logProto := logRecordToProto(record)

		value, err := marshalProtobuf(logProto)
		if err != nil {
			return fmt.Errorf("failed to marshal log record to protobuf: %w", err)
		}

		kafkaRecord := &kgo.Record{
			Topic: e.config.topic,
			Key:   nil,
			Value: value,
		}

		// Add resource attributes as Kafka headers (same as JSON format)
		if e.config.resource != nil {
			kafkaRecord.Headers = resourceToHeaders(e.config.resource)
		}

		kafkaRecords = append(kafkaRecords, kafkaRecord)
	}

	return e.produceBatch(ctx, kafkaRecords)
}

// ForceFlush flushes any pending log records.
func (e *LogExporter) ForceFlush(ctx context.Context) error {
	return e.client.Flush(ctx)
}

// Shutdown shuts down the exporter.
func (e *LogExporter) Shutdown(ctx context.Context) error {
	return e.Exporter.Shutdown(ctx)
}

// logRecordToMap converts a log record to a map for JSON serialization
func (e *LogExporter) logRecordToMap(record sdklog.Record) map[string]any {
	result := map[string]any{
		"timeUnixNano":         fmt.Sprintf("%d", record.Timestamp().UnixNano()),
		"observedTimeUnixNano": fmt.Sprintf("%d", record.ObservedTimestamp().UnixNano()),
		"severityNumber":       int(record.Severity()),
		"severityText":         record.SeverityText(),
		"body":                 e.logValueToAnyValue(record.Body()),
	}

	// Add attributes as array of {key, value} objects per OTLP spec
	attrs := make([]map[string]any, 0)
	record.WalkAttributes(func(kv log.KeyValue) bool {
		attrs = append(attrs, map[string]any{
			"key":   kv.Key,
			"value": e.logValueToAnyValue(kv.Value),
		})
		return true
	})
	if len(attrs) > 0 {
		result["attributes"] = attrs
	}

	// Add trace context if present
	if record.TraceID().IsValid() {
		result["traceId"] = record.TraceID().String()
	}
	if record.SpanID().IsValid() {
		result["spanId"] = record.SpanID().String()
	}
	// Add trace flags as integer (not string) per OTLP spec
	result["flags"] = uint32(record.TraceFlags())

	// Add instrumentation scope
	scope := record.InstrumentationScope()
	if scope.Name != "" {
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

// logValueToAnyValue converts a log.Value to OTLP JSON AnyValue format
// Per OTLP spec, values must be wrapped in typed fields like stringValue, intValue, etc.
func (e *LogExporter) logValueToAnyValue(v log.Value) map[string]any {
	switch v.Kind() {
	case log.KindEmpty:
		return map[string]any{}
	case log.KindBool:
		return map[string]any{"boolValue": v.AsBool()}
	case log.KindFloat64:
		return map[string]any{"doubleValue": v.AsFloat64()}
	case log.KindInt64:
		return map[string]any{"intValue": fmt.Sprintf("%d", v.AsInt64())}
	case log.KindBytes:
		return map[string]any{"bytesValue": v.AsBytes()}
	case log.KindSlice:
		slice := v.AsSlice()
		arrayValues := make([]map[string]any, len(slice))
		for i, item := range slice {
			arrayValues[i] = e.logValueToAnyValue(item)
		}
		return map[string]any{"arrayValue": map[string]any{"values": arrayValues}}
	case log.KindMap:
		kvs := v.AsMap()
		kvList := make([]map[string]any, len(kvs))
		for i, kv := range kvs {
			kvList[i] = map[string]any{
				"key":   kv.Key,
				"value": e.logValueToAnyValue(kv.Value),
			}
		}
		return map[string]any{"kvlistValue": map[string]any{"values": kvList}}
	default:
		// Handles log.KindString and any other unknown types by converting to string
		return map[string]any{"stringValue": v.AsString()}
	}
}

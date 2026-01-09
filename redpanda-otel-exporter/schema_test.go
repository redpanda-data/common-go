// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package redpandaotelexporter

import (
	"encoding/json"
	"testing"

	proto "buf.build/gen/go/redpandadata/otel/protocolbuffers/go/redpanda/otel/v1"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestTraceJSONSchemaMatchesProtojson(t *testing.T) {
	// Create a sample Span message
	span := &proto.Span{
		TraceId:           []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanId:            []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		ParentSpanId:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00},
		Name:              "test-span",
		Kind:              1,
		StartTimeUnixNano: 1234567890,
		EndTimeUnixNano:   1234567900,
		Flags:             1,
		Resource: &proto.Resource{
			Attributes: []*proto.KeyValue{
				{Key: "service.name", Value: &proto.AnyValue{Value: &proto.AnyValue_StringValue{StringValue: "test-service"}}},
			},
			DroppedAttributesCount: 0,
		},
		ResourceSchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		Scope: &proto.InstrumentationScope{
			Name:    "test-scope",
			Version: "1.0.0",
		},
		ScopeSchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		Attributes: []*proto.KeyValue{
			{Key: "http.method", Value: &proto.AnyValue{Value: &proto.AnyValue_StringValue{StringValue: "GET"}}},
		},
		Events: []*proto.Span_Event{
			{
				TimeUnixNano: 1234567895,
				Name:         "test-event",
			},
		},
		Links: []*proto.Span_Link{
			{
				TraceId: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
				SpanId:  []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
			},
		},
		Status: &proto.Status{
			Code:    proto.Status_STATUS_CODE_OK,
			Message: "success",
		},
	}

	// Serialize to JSON using protojson
	jsonData, err := marshalJSON(span)
	if err != nil {
		t.Fatalf("Failed to marshal span to JSON: %v", err)
	}

	// Parse JSON into a generic map
	var data any
	if err := json.Unmarshal(jsonData, &data); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Parse the JSON schema
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(otlpTraceJSONSchema), &schema); err != nil {
		t.Fatalf("Failed to unmarshal trace schema: %v", err)
	}

	// Resolve schema references
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Validate the data against the schema
	if err := resolved.Validate(data); err != nil {
		t.Errorf("JSON schema validation failed: %v", err)
		t.Logf("Generated JSON: %s", string(jsonData))
	}
}

func TestMetricJSONSchemaMatchesProtojson(t *testing.T) {
	// Create a sample Metric message
	metric := &proto.Metric{
		Name:        "test-metric",
		Description: "A test metric",
		Unit:        "1",
		Resource: &proto.Resource{
			Attributes: []*proto.KeyValue{
				{Key: "service.name", Value: &proto.AnyValue{Value: &proto.AnyValue_StringValue{StringValue: "test-service"}}},
			},
		},
		ResourceSchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		Scope: &proto.InstrumentationScope{
			Name:    "test-scope",
			Version: "1.0.0",
		},
		ScopeSchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		Data: &proto.Metric_Gauge{
			Gauge: &proto.Gauge{
				DataPoints: []*proto.NumberDataPoint{
					{
						TimeUnixNano: 1234567890,
						Value:        &proto.NumberDataPoint_AsDouble{AsDouble: 42.5},
					},
				},
			},
		},
	}

	// Serialize to JSON using protojson
	jsonData, err := marshalJSON(metric)
	if err != nil {
		t.Fatalf("Failed to marshal metric to JSON: %v", err)
	}

	// Parse JSON into a generic map
	var data any
	if err := json.Unmarshal(jsonData, &data); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Parse the JSON schema
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(otlpMetricJSONSchema), &schema); err != nil {
		t.Fatalf("Failed to unmarshal metric schema: %v", err)
	}

	// Resolve schema references
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Validate the data against the schema
	if err := resolved.Validate(data); err != nil {
		t.Errorf("JSON schema validation failed: %v", err)
		t.Logf("Generated JSON: %s", string(jsonData))
	}
}

func TestLogJSONSchemaMatchesProtojson(t *testing.T) {
	// Create a sample LogRecord message
	logRecord := &proto.LogRecord{
		TimeUnixNano:         1234567890,
		ObservedTimeUnixNano: 1234567891,
		SeverityNumber:       proto.SeverityNumber_SEVERITY_NUMBER_INFO,
		SeverityText:         "INFO",
		Body: &proto.AnyValue{
			Value: &proto.AnyValue_StringValue{StringValue: "test log message"},
		},
		Resource: &proto.Resource{
			Attributes: []*proto.KeyValue{
				{Key: "service.name", Value: &proto.AnyValue{Value: &proto.AnyValue_StringValue{StringValue: "test-service"}}},
			},
		},
		ResourceSchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		Scope: &proto.InstrumentationScope{
			Name:    "test-scope",
			Version: "1.0.0",
		},
		ScopeSchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		Attributes: []*proto.KeyValue{
			{Key: "log.level", Value: &proto.AnyValue{Value: &proto.AnyValue_StringValue{StringValue: "info"}}},
		},
		TraceId: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
		SpanId:  []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		Flags:   1,
	}

	// Serialize to JSON using protojson
	jsonData, err := marshalJSON(logRecord)
	if err != nil {
		t.Fatalf("Failed to marshal log record to JSON: %v", err)
	}

	// Parse JSON into a generic map
	var data any
	if err := json.Unmarshal(jsonData, &data); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Parse the JSON schema
	var schema jsonschema.Schema
	if err := json.Unmarshal([]byte(otlpLogJSONSchema), &schema); err != nil {
		t.Fatalf("Failed to unmarshal log schema: %v", err)
	}

	// Resolve schema references
	resolved, err := schema.Resolve(nil)
	if err != nil {
		t.Fatalf("Failed to resolve schema: %v", err)
	}

	// Validate the data against the schema
	if err := resolved.Validate(data); err != nil {
		t.Errorf("JSON schema validation failed: %v", err)
		t.Logf("Generated JSON: %s", string(jsonData))
	}
}

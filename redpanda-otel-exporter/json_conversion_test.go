package redpandaotelexporter

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// TestSpanToMap_JSON validates the JSON structure of exported spans
func TestSpanToMap_JSON(t *testing.T) {
	// Create a tracer provider
	tp := sdktrace.NewTracerProvider()
	tracer := tp.Tracer("test-tracer", trace.WithInstrumentationVersion("v1.0.0"))

	// Create a span with all fields populated
	ctx := context.Background()
	_, span := tracer.Start(ctx, "test-span",
		trace.WithAttributes(
			attribute.String("string.attr", "value"),
			attribute.Int64("int.attr", 42),
			attribute.Float64("float.attr", 3.14),
			attribute.Bool("bool.attr", true),
		),
		trace.WithSpanKind(trace.SpanKindServer),
	)
	span.AddEvent("test-event",
		trace.WithAttributes(
			attribute.String("event.attr", "event-value"),
		),
	)
	span.SetStatus(codes.Ok, "success")
	span.End()

	// Convert to readonly span and then to map
	readOnlySpan, ok := span.(sdktrace.ReadOnlySpan)
	require.True(t, ok, "span does not implement ReadOnlySpan interface")
	spanMap := spanToMap(readOnlySpan)

	// Marshal to JSON to see the output
	actualJSON, err := json.MarshalIndent(spanMap, "", "  ")
	require.NoError(t, err)
	t.Logf("Span JSON output:\n%s", string(actualJSON))

	// Extract dynamic values to build expected JSON
	traceID := spanMap["traceId"]
	spanID := spanMap["spanId"]
	startTime := spanMap["startTimeUnixNano"]
	endTime := spanMap["endTimeUnixNano"]
	events, ok := spanMap["events"].([]map[string]any)
	require.True(t, ok, "events should be array")
	eventTime := events[0]["timeUnixNano"]

	// Verify entire structure using JSONEq with dynamic values
	expectedJSON := fmt.Sprintf(`{
		"name": "test-span",
		"traceId": "%s",
		"spanId": "%s",
		"kind": 2,
		"startTimeUnixNano": "%s",
		"endTimeUnixNano": "%s",
		"attributes": [
			{"key": "string.attr", "value": {"stringValue": "value"}},
			{"key": "int.attr", "value": {"intValue": "42"}},
			{"key": "float.attr", "value": {"doubleValue": 3.14}},
			{"key": "bool.attr", "value": {"boolValue": true}}
		],
		"events": [
			{
				"name": "test-event",
				"timeUnixNano": "%s",
				"attributes": [
					{"key": "event.attr", "value": {"stringValue": "event-value"}}
				]
			}
		],
		"status": {
			"code": 2,
			"message": ""
		},
		"flags": 1,
		"instrumentationScope": {
			"name": "test-tracer",
			"version": "v1.0.0"
		}
	}`, traceID, spanID, startTime, endTime, eventTime)

	require.JSONEq(t, expectedJSON, string(actualJSON))
}

// TestMetricToMap_JSON validates the JSON structure of exported metrics
func TestMetricToMap_JSON(t *testing.T) {
	// Create a gauge metric
	now := time.Now()
	gauge := metricdata.Metrics{
		Name:        "test.gauge",
		Description: "A test gauge metric",
		Unit:        "1",
		Data: metricdata.Gauge[float64]{
			DataPoints: []metricdata.DataPoint[float64]{
				{
					Attributes: attribute.NewSet(
						attribute.String("label", "value"),
					),
					Time:      now,
					StartTime: now.Add(-time.Minute),
					Value:     42.5,
				},
			},
		},
	}

	// Convert to map
	exporter := &MetricExporter{}
	metricMap := exporter.metricToMap(gauge, instrumentation.Scope{
		Name:    "test-meter",
		Version: "v1.0.0",
	})

	// Marshal to JSON
	actualJSON, err := json.MarshalIndent(metricMap, "", "  ")
	require.NoError(t, err)
	t.Logf("Gauge metric JSON output:\n%s", string(actualJSON))

	// Extract dynamic values to build expected JSON
	dataPoints, ok := metricMap["dataPoints"].([]map[string]any)
	require.True(t, ok, "dataPoints should be array")
	startTime := dataPoints[0]["startTimeUnixNano"]
	time := dataPoints[0]["timeUnixNano"]

	// Verify entire structure using JSONEq with dynamic values
	expectedJSON := fmt.Sprintf(`{
		"name": "test.gauge",
		"description": "A test gauge metric",
		"unit": "1",
		"type": "gauge",
		"scope": {
			"name": "test-meter",
			"version": "v1.0.0"
		},
		"dataPoints": [
			{
				"attributes": [
					{"key": "label", "value": {"stringValue": "value"}}
				],
				"startTimeUnixNano": "%s",
				"timeUnixNano": "%s",
				"value": 42.5,
				"exemplars": null
			}
		]
	}`, startTime, time)

	require.JSONEq(t, expectedJSON, string(actualJSON))
}

// TestLogRecordToMap_JSON validates the JSON structure of exported logs
func TestLogRecordToMap_JSON(t *testing.T) {
	// Create a log exporter
	exporter := &LogExporter{}

	// Create a log record
	var record sdklog.Record
	record.SetTimestamp(time.Now())
	record.SetObservedTimestamp(time.Now())
	record.SetSeverity(log.SeverityInfo)
	record.SetSeverityText("INFO")
	record.SetBody(log.StringValue("test log message"))
	record.AddAttributes(
		log.String("key", "value"),
		log.Int64("count", 42),
	)

	// Convert to map
	logMap := exporter.logRecordToMap(record)

	// Marshal to JSON
	actualJSON, err := json.MarshalIndent(logMap, "", "  ")
	require.NoError(t, err)
	t.Logf("Log record JSON output:\n%s", string(actualJSON))

	// Extract dynamic values to build expected JSON
	timeUnixNano := logMap["timeUnixNano"]
	observedTimeUnixNano := logMap["observedTimeUnixNano"]

	// Verify entire structure using JSONEq with dynamic values
	expectedJSON := fmt.Sprintf(`{
		"timeUnixNano": "%s",
		"observedTimeUnixNano": "%s",
		"severityNumber": 9,
		"severityText": "INFO",
		"body": {
			"stringValue": "test log message"
		},
		"attributes": [
			{"key": "key", "value": {"stringValue": ""}},
			{"key": "count", "value": {"intValue": "42"}}
		],
		"flags": 0
	}`, timeUnixNano, observedTimeUnixNano)

	require.JSONEq(t, expectedJSON, string(actualJSON))
}

// TestAttributeConversion_AllTypes validates all attribute type conversions
func TestAttributeConversion_AllTypes(t *testing.T) {
	testCases := []struct {
		name         string
		attr         attribute.KeyValue
		expectedJSON string
	}{
		{
			name: "string",
			attr: attribute.String("key", "value"),
			expectedJSON: `{
				"key": "key",
				"value": {
					"stringValue": "value"
				}
			}`,
		},
		{
			name: "int64",
			attr: attribute.Int64("key", 42),
			expectedJSON: `{
				"key": "key",
				"value": {
					"intValue": "42"
				}
			}`,
		},
		{
			name: "float64",
			attr: attribute.Float64("key", 3.14),
			expectedJSON: `{
				"key": "key",
				"value": {
					"doubleValue": 3.14
				}
			}`,
		},
		{
			name: "bool",
			attr: attribute.Bool("key", true),
			expectedJSON: `{
				"key": "key",
				"value": {
					"boolValue": true
				}
			}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := attributesToArray([]attribute.KeyValue{tc.attr})
			require.Len(t, result, 1)

			actualJSON, err := json.Marshal(result[0])
			require.NoError(t, err)
			t.Logf("Attribute %s JSON output:\n%s", tc.name, string(actualJSON))

			require.JSONEq(t, tc.expectedJSON, string(actualJSON))
		})
	}
}

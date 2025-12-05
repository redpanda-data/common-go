package redpandaotelexporter

import _ "embed"

// OTLP Protobuf schemas - these are the official OTLP proto definitions

//go:embed proto/common.proto
var otlpCommonProtoSchema string

//go:embed proto/trace.proto
var otlpTraceProtoSchema string

//go:embed proto/metric.proto
var otlpMetricProtoSchema string

//go:embed proto/log.proto
var otlpLogProtoSchema string

// OTLP JSON schemas - JSON Schema definitions for OTLP JSON format

const otlpTraceJSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "redpanda.otel.v1.Span",
  "title": "Span",
  "description": "A Span represents a single operation performed by a single component of the system.",
  "type": "object",
  "properties": {
    "resource": {
      "type": "object",
      "description": "The resource for the span.",
      "properties": {
        "attributes": {"type": "array"},
        "droppedAttributesCount": {"type": "integer", "minimum": 0}
      }
    },
    "resourceSchemaUrl": {"type": "string"},
    "scope": {
      "type": "object",
      "description": "The instrumentation scope information",
      "properties": {
        "name": {"type": "string"},
        "version": {"type": "string"},
        "attributes": {"type": "array"},
        "droppedAttributesCount": {"type": "integer", "minimum": 0}
      }
    },
    "scopeSchemaUrl": {"type": "string"},
    "traceId": {
      "type": "string",
      "description": "A unique identifier for a trace. 16-byte array encoded as hex string.",
      "pattern": "^[0-9a-fA-F]{32}$"
    },
    "spanId": {
      "type": "string",
      "description": "A unique identifier for a span within a trace. 8-byte array encoded as hex string.",
      "pattern": "^[0-9a-fA-F]{16}$"
    },
    "traceState": {"type": "string"},
    "parentSpanId": {
      "type": "string",
      "pattern": "^[0-9a-fA-F]{16}$"
    },
    "flags": {"type": "integer", "minimum": 0},
    "name": {"type": "string"},
    "kind": {
      "type": "integer",
      "enum": [0, 1, 2, 3, 4, 5]
    },
    "startTimeUnixNano": {
      "type": "string",
      "pattern": "^[0-9]+$"
    },
    "endTimeUnixNano": {
      "type": "string",
      "pattern": "^[0-9]+$"
    },
    "attributes": {"type": "array"},
    "droppedAttributesCount": {"type": "integer", "minimum": 0},
    "events": {"type": "array"},
    "droppedEventsCount": {"type": "integer", "minimum": 0},
    "links": {"type": "array"},
    "droppedLinksCount": {"type": "integer", "minimum": 0},
    "status": {
      "type": "object",
      "properties": {
        "message": {"type": "string"},
        "code": {"type": "integer", "enum": [0, 1, 2]}
      }
    }
  }
}`

const otlpMetricJSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "redpanda.otel.v1.Metric",
  "title": "Metric",
  "description": "Defines a Metric which has one or more timeseries",
  "type": "object",
  "properties": {
    "resource": {
      "type": "object",
      "properties": {
        "attributes": {"type": "array"},
        "droppedAttributesCount": {"type": "integer", "minimum": 0}
      }
    },
    "resourceSchemaUrl": {"type": "string"},
    "scope": {
      "type": "object",
      "properties": {
        "name": {"type": "string"},
        "version": {"type": "string"},
        "attributes": {"type": "array"},
        "droppedAttributesCount": {"type": "integer", "minimum": 0}
      }
    },
    "scopeSchemaUrl": {"type": "string"},
    "name": {"type": "string"},
    "description": {"type": "string"},
    "unit": {"type": "string"},
    "gauge": {
      "type": "object",
      "properties": {
        "dataPoints": {"type": "array"}
      }
    },
    "sum": {
      "type": "object",
      "properties": {
        "dataPoints": {"type": "array"},
        "aggregationTemporality": {"type": "integer", "enum": [0, 1, 2]},
        "isMonotonic": {"type": "boolean"}
      }
    },
    "histogram": {
      "type": "object",
      "properties": {
        "dataPoints": {"type": "array"},
        "aggregationTemporality": {"type": "integer", "enum": [0, 1, 2]}
      }
    },
    "exponentialHistogram": {
      "type": "object",
      "properties": {
        "dataPoints": {"type": "array"},
        "aggregationTemporality": {"type": "integer", "enum": [0, 1, 2]}
      }
    },
    "summary": {
      "type": "object",
      "properties": {
        "dataPoints": {"type": "array"}
      }
    },
    "metadata": {"type": "array"}
  }
}`

const otlpLogJSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "redpanda.otel.v1.LogRecord",
  "title": "LogRecord",
  "description": "A log record according to OpenTelemetry Log Data Model",
  "type": "object",
  "properties": {
    "resource": {
      "type": "object",
      "properties": {
        "attributes": {"type": "array"},
        "droppedAttributesCount": {"type": "integer", "minimum": 0}
      }
    },
    "resourceSchemaUrl": {"type": "string"},
    "scope": {
      "type": "object",
      "properties": {
        "name": {"type": "string"},
        "version": {"type": "string"},
        "attributes": {"type": "array"},
        "droppedAttributesCount": {"type": "integer", "minimum": 0}
      }
    },
    "scopeSchemaUrl": {"type": "string"},
    "timeUnixNano": {
      "type": "string",
      "pattern": "^[0-9]+$"
    },
    "observedTimeUnixNano": {
      "type": "string",
      "pattern": "^[0-9]+$"
    },
    "severityNumber": {
      "type": "integer",
      "minimum": 0,
      "maximum": 24
    },
    "severityText": {"type": "string"},
    "body": {"type": "object"},
    "attributes": {"type": "array"},
    "droppedAttributesCount": {"type": "integer", "minimum": 0},
    "flags": {"type": "integer", "minimum": 0},
    "traceId": {
      "type": "string",
      "pattern": "^[0-9a-fA-F]{32}$"
    },
    "spanId": {
      "type": "string",
      "pattern": "^[0-9a-fA-F]{16}$"
    },
    "eventName": {"type": "string"}
  }
}`

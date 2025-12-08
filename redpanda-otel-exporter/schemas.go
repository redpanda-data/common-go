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

//go:embed proto/trace.schema.json
var otlpTraceJSONSchema string

//go:embed proto/metric.schema.json
var otlpMetricJSONSchema string

//go:embed proto/log.schema.json
var otlpLogJSONSchema string

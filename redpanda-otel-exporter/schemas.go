package redpandaotelexporter

// OTLP Protobuf schemas - these are the official OTLP proto definitions

const otlpTraceProtoSchema = `syntax = "proto3";

package opentelemetry.proto.trace.v1;

message Span {
  bytes trace_id = 1;
  bytes span_id = 2;
  string trace_state = 3;
  bytes parent_span_id = 4;
  string name = 5;
  enum SpanKind {
    SPAN_KIND_UNSPECIFIED = 0;
    SPAN_KIND_INTERNAL = 1;
    SPAN_KIND_SERVER = 2;
    SPAN_KIND_CLIENT = 3;
    SPAN_KIND_PRODUCER = 4;
    SPAN_KIND_CONSUMER = 5;
  }
  SpanKind kind = 6;
  fixed64 start_time_unix_nano = 7;
  fixed64 end_time_unix_nano = 8;
  repeated KeyValue attributes = 9;
  uint32 dropped_attributes_count = 10;
  repeated Event events = 11;
  uint32 dropped_events_count = 12;
  repeated Link links = 13;
  uint32 dropped_links_count = 14;
  Status status = 15;
  fixed32 flags = 16;
}

message Status {
  enum StatusCode {
    STATUS_CODE_UNSET = 0;
    STATUS_CODE_OK = 1;
    STATUS_CODE_ERROR = 2;
  }
  string message = 2;
  StatusCode code = 3;
}

message KeyValue {
  string key = 1;
  AnyValue value = 2;
}

message AnyValue {
  oneof value {
    string string_value = 1;
    bool bool_value = 2;
    int64 int_value = 3;
    double double_value = 4;
    ArrayValue array_value = 5;
    KeyValueList kvlist_value = 6;
    bytes bytes_value = 7;
  }
}

message ArrayValue {
  repeated AnyValue values = 1;
}

message KeyValueList {
  repeated KeyValue values = 1;
}

message Event {
  fixed64 time_unix_nano = 1;
  string name = 2;
  repeated KeyValue attributes = 3;
  uint32 dropped_attributes_count = 4;
}

message Link {
  bytes trace_id = 1;
  bytes span_id = 2;
  string trace_state = 3;
  repeated KeyValue attributes = 4;
  uint32 dropped_attributes_count = 5;
  fixed32 flags = 6;
}
`

const otlpMetricProtoSchema = `syntax = "proto3";

package opentelemetry.proto.metrics.v1;

message Metric {
  string name = 1;
  string description = 2;
  string unit = 3;
  oneof data {
    Gauge gauge = 5;
    Sum sum = 7;
    Histogram histogram = 9;
    ExponentialHistogram exponential_histogram = 10;
    Summary summary = 11;
  }
}

message Gauge {
  repeated NumberDataPoint data_points = 1;
}

message Sum {
  repeated NumberDataPoint data_points = 1;
  enum AggregationTemporality {
    AGGREGATION_TEMPORALITY_UNSPECIFIED = 0;
    AGGREGATION_TEMPORALITY_DELTA = 1;
    AGGREGATION_TEMPORALITY_CUMULATIVE = 2;
  }
  AggregationTemporality aggregation_temporality = 2;
  bool is_monotonic = 3;
}

message Histogram {
  repeated HistogramDataPoint data_points = 1;
  enum AggregationTemporality {
    AGGREGATION_TEMPORALITY_UNSPECIFIED = 0;
    AGGREGATION_TEMPORALITY_DELTA = 1;
    AGGREGATION_TEMPORALITY_CUMULATIVE = 2;
  }
  AggregationTemporality aggregation_temporality = 2;
}

message ExponentialHistogram {
  repeated ExponentialHistogramDataPoint data_points = 1;
  enum AggregationTemporality {
    AGGREGATION_TEMPORALITY_UNSPECIFIED = 0;
    AGGREGATION_TEMPORALITY_DELTA = 1;
    AGGREGATION_TEMPORALITY_CUMULATIVE = 2;
  }
  AggregationTemporality aggregation_temporality = 2;
}

message Summary {
  repeated SummaryDataPoint data_points = 1;
}

message NumberDataPoint {
  repeated KeyValue attributes = 7;
  fixed64 start_time_unix_nano = 2;
  fixed64 time_unix_nano = 3;
  oneof value {
    double as_double = 4;
    sfixed64 as_int = 6;
  }
  repeated Exemplar exemplars = 5;
  uint32 flags = 8;
}

message HistogramDataPoint {
  repeated KeyValue attributes = 9;
  fixed64 start_time_unix_nano = 2;
  fixed64 time_unix_nano = 3;
  uint64 count = 4;
  optional double sum = 5;
  repeated uint64 bucket_counts = 6;
  repeated double explicit_bounds = 7;
  repeated Exemplar exemplars = 8;
  uint32 flags = 10;
  optional double min = 11;
  optional double max = 12;
}

message ExponentialHistogramDataPoint {
  repeated KeyValue attributes = 1;
  fixed64 start_time_unix_nano = 2;
  fixed64 time_unix_nano = 3;
  uint64 count = 4;
  optional double sum = 5;
  sint32 scale = 6;
  uint64 zero_count = 7;
  Buckets positive = 8;
  Buckets negative = 9;
  uint32 flags = 10;
  repeated Exemplar exemplars = 11;
  optional double min = 12;
  optional double max = 13;
  double zero_threshold = 14;

  message Buckets {
    sint32 offset = 1;
    repeated uint64 bucket_counts = 2;
  }
}

message SummaryDataPoint {
  repeated KeyValue attributes = 7;
  fixed64 start_time_unix_nano = 2;
  fixed64 time_unix_nano = 3;
  uint64 count = 4;
  double sum = 5;
  repeated ValueAtQuantile quantile_values = 6;
  uint32 flags = 8;

  message ValueAtQuantile {
    double quantile = 1;
    double value = 2;
  }
}

message Exemplar {
  repeated KeyValue filtered_attributes = 7;
  fixed64 time_unix_nano = 2;
  oneof value {
    double as_double = 3;
    sfixed64 as_int = 6;
  }
  bytes span_id = 4;
  bytes trace_id = 5;
}

message KeyValue {
  string key = 1;
  AnyValue value = 2;
}

message AnyValue {
  oneof value {
    string string_value = 1;
    bool bool_value = 2;
    int64 int_value = 3;
    double double_value = 4;
  }
}
`

const otlpLogProtoSchema = `syntax = "proto3";

package opentelemetry.proto.logs.v1;

message LogRecord {
  fixed64 time_unix_nano = 1;
  fixed64 observed_time_unix_nano = 11;
  enum SeverityNumber {
    SEVERITY_NUMBER_UNSPECIFIED = 0;
    SEVERITY_NUMBER_TRACE = 1;
    SEVERITY_NUMBER_TRACE2 = 2;
    SEVERITY_NUMBER_TRACE3 = 3;
    SEVERITY_NUMBER_TRACE4 = 4;
    SEVERITY_NUMBER_DEBUG = 5;
    SEVERITY_NUMBER_DEBUG2 = 6;
    SEVERITY_NUMBER_DEBUG3 = 7;
    SEVERITY_NUMBER_DEBUG4 = 8;
    SEVERITY_NUMBER_INFO = 9;
    SEVERITY_NUMBER_INFO2 = 10;
    SEVERITY_NUMBER_INFO3 = 11;
    SEVERITY_NUMBER_INFO4 = 12;
    SEVERITY_NUMBER_WARN = 13;
    SEVERITY_NUMBER_WARN2 = 14;
    SEVERITY_NUMBER_WARN3 = 15;
    SEVERITY_NUMBER_WARN4 = 16;
    SEVERITY_NUMBER_ERROR = 17;
    SEVERITY_NUMBER_ERROR2 = 18;
    SEVERITY_NUMBER_ERROR3 = 19;
    SEVERITY_NUMBER_ERROR4 = 20;
    SEVERITY_NUMBER_FATAL = 21;
    SEVERITY_NUMBER_FATAL2 = 22;
    SEVERITY_NUMBER_FATAL3 = 23;
    SEVERITY_NUMBER_FATAL4 = 24;
  }
  SeverityNumber severity_number = 2;
  string severity_text = 3;
  AnyValue body = 5;
  repeated KeyValue attributes = 6;
  uint32 dropped_attributes_count = 7;
  uint32 flags = 8;
  bytes trace_id = 9;
  bytes span_id = 10;
}

message KeyValue {
  string key = 1;
  AnyValue value = 2;
}

message AnyValue {
  oneof value {
    string string_value = 1;
    bool bool_value = 2;
    int64 int_value = 3;
    double double_value = 4;
    ArrayValue array_value = 5;
    KeyValueList kvlist_value = 6;
    bytes bytes_value = 7;
  }
}

message ArrayValue {
  repeated AnyValue values = 1;
}

message KeyValueList {
  repeated KeyValue values = 1;
}
`

// OTLP JSON schemas - JSON Schema definitions for OTLP JSON format

const otlpTraceJSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "traceId": {"type": "string", "pattern": "^[0-9a-fA-F]{32}$"},
    "spanId": {"type": "string", "pattern": "^[0-9a-fA-F]{16}$"},
    "parentSpanId": {"type": "string", "pattern": "^[0-9a-fA-F]{16}$"},
    "kind": {"type": "integer"},
    "startTimeUnixNano": {"type": "string"},
    "endTimeUnixNano": {"type": "string"},
    "attributes": {"type": "array"},
    "status": {"type": "object"},
    "events": {"type": "array"},
    "links": {"type": "array"}
  }
}`

const otlpMetricJSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "description": {"type": "string"},
    "unit": {"type": "string"},
    "type": {"type": "string"},
    "dataPoints": {"type": "array"}
  }
}`

const otlpLogJSONSchema = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "timeUnixNano": {"type": "string"},
    "observedTimeUnixNano": {"type": "string"},
    "severityNumber": {"type": "integer"},
    "severityText": {"type": "string"},
    "body": {"type": "object"},
    "attributes": {"type": "array"}
  }
}`

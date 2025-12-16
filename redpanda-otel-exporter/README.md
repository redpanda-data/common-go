# Redpanda OpenTelemetry Exporter

A Go OpenTelemetry exporter that exports traces, metrics, and logs to Kafka/Redpanda using [franz-go](https://github.com/twmb/franz-go).

## Features

- **Full OpenTelemetry Support**: Implements exporters for traces, metrics, and logs
- **High Performance**: Built on franz-go, one of the fastest Kafka clients for Go
- **Flexible Configuration**: Supports custom Kafka client options
- **Multiple Serialization Formats**: Choose between JSON, Protobuf (OTLP standard), or Schema Registry serdes format
- **Schema Registry Integration**: Native support for Confluent Schema Registry with automatic schema management
- **Resource Attributes**: Includes service and resource information with telemetry data
- **Batch Processing**: Efficiently batches records for optimal throughput
- **Optimized for Modern Data Pipelines**: One signal per Kafka record for seamless integration with Iceberg tables and other streaming platforms

## Architecture

This exporter differs from the standard OpenTelemetry Collector Kafka exporter in its data model:

**One Signal Per Record**: Each span, metric, or log is written as an individual Kafka record, rather than batching multiple signals into a single record. This design enables:
- Direct integration with Iceberg topics without additional processing
- Efficient partitioning (by trace ID for spans, metric name for metrics)
- Simpler downstream consumption patterns
- Better parallelism and scalability

**Resource and Scope Embedded**: Resource attributes (service.name, service.version, etc.) and instrumentation scope information are included directly in each message body. This:
- Ensures complete context for each signal
- Maintains OTLP compatibility
- Enables self-contained records that can be processed independently
- Supports both JSON and binary protobuf serialization with identical structure

## Data Format

Telemetry data can be exported in multiple formats:
- **JSON**: Human-readable format using protobuf JSON encoding (protojson)
- **Protobuf**: Binary format using custom OTLP-compatible protobuf definitions
- **Schema Registry JSON**: JSON with Schema Registry serdes encoding
- **Schema Registry Protobuf**: Protobuf with Schema Registry serdes encoding

Each Kafka record contains a single telemetry signal (one span, one metric, or one log record).

### Protobuf Schema

This exporter uses custom protobuf definitions located in the `./proto` directory that are compatible with the OpenTelemetry Protocol (OTLP) format. The schemas are defined in:
- `proto/trace.proto` - Trace/span messages
- `proto/metric.proto` - Metric messages
- `proto/log.proto` - Log record messages
- `proto/common.proto` - Common types (KeyValue, AnyValue, Resource, etc.)

These schemas include resource and scope information directly in each message, enabling one-signal-per-record export pattern.

**Note**: These protobuf schemas are published to the Buf Schema Registry at [buf.build/redpandadata/otel](https://buf.build/redpandadata/otel) as package `redpanda.otel.v1`. Updates to the schemas must be manually pushed to BSR using `buf push`.

### JSON Format Examples

The JSON format is generated using `protojson.Marshal()` from the protobuf messages, ensuring consistency between JSON and binary formats:
- **IDs**: Base64-encoded byte arrays (traceId, spanId)
- **Timestamps**: String-encoded uint64 nanoseconds (to avoid precision loss)
- **Enums**: Integer values (not string names)
- **Attributes**: Array of objects with typed values (stringValue, intValue, etc.)
- **Resource and Scope**: Included directly in each message

#### Trace Format (Single Span)

```json
{
  "resource": {
    "attributes": [
      {"key": "service.name", "value": {"stringValue": "my-service"}},
      {"key": "service.version", "value": {"stringValue": "1.0.0"}},
      {"key": "deployment.environment", "value": {"stringValue": "production"}}
    ]
  },
  "resourceSchemaUrl": "https://opentelemetry.io/schemas/1.24.0",
  "scope": {
    "name": "my-tracer",
    "version": "1.0.0"
  },
  "scopeSchemaUrl": "",
  "traceId": "S/kvNXezTaajzpKdDg5HNg==",
  "spanId": "APBnqguqkLc=",
  "name": "HTTP GET /api/users",
  "kind": 2,
  "startTimeUnixNano": "1544712660300000000",
  "endTimeUnixNano": "1544712660600000000",
  "attributes": [
    {"key": "http.method", "value": {"stringValue": "GET"}},
    {"key": "http.status_code", "value": {"intValue": "200"}}
  ],
  "status": {
    "code": 1
  }
}
```

#### Metric Format (Single Metric)

```json
{
  "resource": {
    "attributes": [
      {"key": "service.name", "value": {"stringValue": "my-service"}},
      {"key": "service.version", "value": {"stringValue": "1.0.0"}}
    ]
  },
  "resourceSchemaUrl": "https://opentelemetry.io/schemas/1.24.0",
  "scope": {
    "name": "my-meter",
    "version": "1.0.0"
  },
  "scopeSchemaUrl": "",
  "name": "http.server.request_count",
  "description": "Total HTTP requests",
  "unit": "{requests}",
  "sum": {
    "dataPoints": [
      {
        "attributes": [
          {"key": "http.route", "value": {"stringValue": "/api/users"}},
          {"key": "http.status_code", "value": {"intValue": "200"}}
        ],
        "startTimeUnixNano": "1544712660000000000",
        "timeUnixNano": "1544712660300000000",
        "asInt": "42"
      }
    ],
    "aggregationTemporality": 2,
    "isMonotonic": true
  }
}
```

#### Log Format (Single Log Record)

```json
{
  "resource": {
    "attributes": [
      {"key": "service.name", "value": {"stringValue": "my-service"}},
      {"key": "service.version", "value": {"stringValue": "1.0.0"}}
    ]
  },
  "resourceSchemaUrl": "https://opentelemetry.io/schemas/1.24.0",
  "scope": {
    "name": "my-logger",
    "version": "1.0.0"
  },
  "scopeSchemaUrl": "",
  "timeUnixNano": "1544712660300000000",
  "observedTimeUnixNano": "1544712660300100000",
  "severityNumber": 9,
  "severityText": "INFO",
  "body": {
    "stringValue": "User login successful"
  },
  "attributes": [
    {"key": "user.id", "value": {"stringValue": "user123"}},
    {"key": "login.attempts", "value": {"intValue": "1"}}
  ],
  "traceId": "S/kvNXezTaajzpKdDg5HNg==",
  "spanId": "APBnqguqkLc=",
  "flags": 1
}
```

### Protobuf Format

When using `SerializationFormatProtobuf`, data is serialized using the custom protobuf definitions in `./proto`:

- **Traces**: `redpanda.otel.v1.Span` (one span per record)
- **Metrics**: `redpanda.otel.v1.Metric` (one metric per record)
- **Logs**: `redpanda.otel.v1.LogRecord` (one log per record)

The protobuf format is OTLP-compatible and includes resource and scope information directly in each message. The binary encoding is more compact than JSON and can be decoded using the proto files in the `./proto` directory.

## Installation

```bash
go get github.com/redpanda-data/common-go/redpanda-otel-exporter
```

## Usage

### Basic Configuration

```go
import (
    exporter "github.com/redpanda-data/common-go/redpanda-otel-exporter"
    "go.opentelemetry.io/otel"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Create trace exporter (uses default topic "otel-traces")
traceExporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithClientID("my-service"),
)
if err != nil {
    log.Fatal(err)
}
defer traceExporter.Shutdown(context.Background())

// Set up trace provider
tracerProvider := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(traceExporter),
)
otel.SetTracerProvider(tracerProvider)
```

### Trace Exporter

The trace exporter exports OpenTelemetry spans to a Kafka topic:

```go
// Uses default topic "otel-traces"
traceExporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithClientID("trace-exporter"),
    exporter.WithTimeout(30 * time.Second),
)

// Or use a custom topic
traceExporter, err := exporter.NewTraceExporter(
    exporter.WithTopic("my-traces"),
    exporter.WithBrokers("localhost:9092"),
)
```

Traces are partitioned by trace ID for efficient querying.

### Metric Exporter

The metric exporter exports OpenTelemetry metrics to a Kafka topic:

```go
// Uses default topic "otel-metrics"
metricExporter, err := exporter.NewMetricExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithClientID("metric-exporter"),
)

meterProvider := sdkmetric.NewMeterProvider(
    sdkmetric.WithReader(
        sdkmetric.NewPeriodicReader(metricExporter,
            sdkmetric.WithInterval(10*time.Second)),
    ),
)
```

Supports all metric types:
- Gauge
- Counter/Sum
- Histogram
- Exponential Histogram
- Summary

### Log Exporter

The log exporter exports OpenTelemetry logs to a Kafka topic:

```go
// Uses default topic "otel-logs"
logExporter, err := exporter.NewLogExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithClientID("log-exporter"),
)

loggerProvider := log.NewLoggerProvider(
    log.WithProcessor(log.NewBatchProcessor(logExporter)),
)
```

### Serialization Formats

The exporter supports four serialization formats:

#### JSON Format (Default)

Human-readable JSON format, ideal for debugging and development:

```go
exporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithSerializationFormat(exporter.SerializationFormatJSON), // or omit for default
)
```

#### Protobuf Format (OTLP)

Binary Protocol Buffers format using the official OTLP specification. More efficient and compact:

```go
exporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithSerializationFormat(exporter.SerializationFormatProtobuf),
)
```

#### Schema Registry JSON Format

JSON format with Confluent Schema Registry serdes encoding. Messages include a magic byte (0x00) and schema ID prefix:

```go
import "github.com/twmb/franz-go/pkg/sr"

exporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithSerializationFormat(exporter.SerializationFormatSchemaRegistryJSON),
    exporter.WithSchemaRegistryURL("http://localhost:8081"),
    // Optional: specify subject name (defaults to topic name)
    exporter.WithSchemaSubject("my-custom-subject"),
    // Optional: add auth or other SR client options
    exporter.WithSchemaRegistryOptions(
        sr.BasicAuth("username", "password"),
    ),
)
```

#### Schema Registry Protobuf Format

Protobuf format with Confluent Schema Registry serdes encoding:

```go
import "github.com/twmb/franz-go/pkg/sr"

exporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithSerializationFormat(exporter.SerializationFormatSchemaRegistryProtobuf),
    exporter.WithSchemaRegistryURL("http://localhost:8081"),
    // Optional: specify subject name (defaults to topic name)
    exporter.WithSchemaSubject("my-custom-subject"),
    // Optional: add auth or other SR client options
    exporter.WithSchemaRegistryOptions(
        sr.BasicAuth("username", "password"),
        sr.HTTPClient(customHTTPClient),
    ),
)
```

**Schema Registry Features:**
- Automatic schema lookup by subject name
- Schema ID encoding in wire format (magic byte + schema ID + payload)
- Compatible with Confluent Schema Registry and Redpanda Schema Registry
- Supports topic naming strategy (subject = topic name) by default
- Optional subject override for custom naming strategies

**Benefits of each format:**
- **JSON**: Development, debugging, human inspection, simple consumers
- **Protobuf**: Production, high throughput, integration with OTLP ecosystem
- **Schema Registry JSON**: Enforced schema validation, JSON consumers, schema evolution
- **Schema Registry Protobuf**: Best of both worlds - compact binary format with schema management

**When to use Schema Registry formats:**
- When you need schema validation and evolution
- When integrating with existing Schema Registry infrastructure
- When consumers require schema metadata
- When you want centralized schema management across multiple services

### Advanced Configuration

You can pass custom franz-go options:

```go
import "github.com/twmb/franz-go/pkg/kgo"

exporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithClientID("my-exporter"),
    exporter.WithKafkaOptions(
        kgo.ProducerBatchCompression(kgo.GzipCompression()),
        kgo.ProducerLinger(200 * time.Millisecond),
        kgo.SASL(saslMechanism),
    ),
)
```

### With Resource Attributes

Resource attributes are automatically included from the OpenTelemetry SDK. Configure them when creating your tracer/meter/logger provider:

```go
import (
    "go.opentelemetry.io/otel/sdk/resource"
    semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

res, err := resource.New(ctx,
    resource.WithAttributes(
        semconv.ServiceName("my-service"),
        semconv.ServiceVersion("1.0.0"),
        attribute.String("environment", "production"),
    ),
)

// Resource attributes are passed through the SDK provider
tracerProvider := sdktrace.NewTracerProvider(
    sdktrace.WithBatcher(exporter),
    sdktrace.WithResource(res),  // <-- Resource configured here
)
```

The exporter automatically extracts resource and scope information from each span/metric/log and includes it in the exported message.

## Example Usage

See `example_test.go` for a complete working example showing how to use all three exporters (traces, metrics, and logs) together. The example demonstrates:
- Creating resource attributes with service information
- Configuring exporters with both JSON and Protobuf formats
- Setting up OpenTelemetry providers
- Emitting telemetry data

To see the example in action:

1. Start Redpanda or Kafka:
```bash
docker run -d --name=redpanda -p 9092:9092 \
  docker.redpanda.com/redpandadata/redpanda:latest \
  redpanda start --smp 1
```

2. Run the example:
```bash
go test -v -run Example
```

3. Consume the data:
```bash
# For traces
rpk topic consume otel-traces

# For metrics
rpk topic consume otel-metrics

# For logs
rpk topic consume otel-logs
```

## Running Tests

### With Testcontainers (Default)

By default, tests use testcontainers to automatically spin up a Redpanda instance:

```bash
go test -v
```

### With Local Redpanda

To use a local Redpanda instance at `localhost:9092` instead of testcontainers:

```bash
# Start local Redpanda
rpk container start

# Run tests against local instance
go test -v -local-broker

# Inspect the data in topics after tests complete
rpk topic consume test-traces-json
rpk topic consume test-traces-protobuf
rpk topic consume test-metrics
rpk topic consume test-logs
```

This is useful for manually inspecting the test data output.

## Configuration Options

All exporters use functional options pattern:

| Option | Description | Default |
|--------|-------------|---------|
| `WithBrokers(brokers ...string)` | Kafka broker addresses (required) | None - must be provided |
| `WithTopic(topic string)` | Kafka topic name | `"otel-traces"`, `"otel-metrics"`, or `"otel-logs"` depending on exporter type |
| `WithClientID(clientID string)` | Kafka client identifier | `"otel-kafka-exporter"` |
| `WithTimeout(timeout time.Duration)` | Export operation timeout | `30s` |
| `WithSerializationFormat(format SerializationFormat)` | Format: JSON, Protobuf, SchemaRegistryJSON, or SchemaRegistryProtobuf | `SerializationFormatJSON` |
| `WithSchemaRegistryURL(url string)` | Schema Registry URL (required for SR formats) | `""` |
| `WithSchemaSubject(subject string)` | Schema subject name override (defaults to topic name) | `""` (uses topic name) |
| `WithSchemaRegistryOptions(opts ...sr.Opt)` | Additional franz-go Schema Registry client options (auth, TLS, etc.) | `[]` |
| `WithKafkaOptions(opts ...kgo.Opt)` | Additional franz-go client options | `[]` |

## Performance Considerations

- The exporter uses batching by default for optimal throughput
- **Traces**: One Kafka record per span, partitioned by trace ID for efficient querying
  - Both JSON and Protobuf formats use the same partitioning strategy
  - Spans from the same trace go to the same partition for better locality
- **Metrics**: Records partitioned by metric name for efficient querying
- Consider adjusting `ProducerLinger` and `ProducerBatchMaxBytes` for your workload
- Use compression for high-volume workloads

## License

This project is part of the Redpanda common-go repository.

## Contributing

Contributions are welcome! Please submit issues and pull requests to the main repository.

## Related Projects

- [franz-go](https://github.com/twmb/franz-go) - High-performance Kafka client
- [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go) - OpenTelemetry SDK for Go
- [Redpanda](https://redpanda.com) - Kafka-compatible streaming platform

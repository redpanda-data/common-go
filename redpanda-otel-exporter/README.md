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

**Resource Attributes in Headers**: Resource attributes (service.name, service.version, etc.) are stored as Kafka record headers rather than in the JSON/Protobuf body. This:
- Reduces message payload size
- Enables efficient filtering at the Kafka level
- Separates signal data from metadata
- Maintains OTLP compliance while optimizing for Kafka

## Data Format

Telemetry data can be exported in multiple formats:
- **JSON**: Human-readable format
- **Protobuf**: Binary OTLP format
- **Schema Registry JSON**: JSON with Schema Registry serdes encoding
- **Schema Registry Protobuf**: Protobuf with Schema Registry serdes encoding

Each Kafka record contains a single telemetry signal (one span, one metric, or one log record).

### JSON Format Examples

The JSON format follows the **OTLP JSON encoding specification** ([opentelemetry-proto v1.9.0](https://github.com/open-telemetry/opentelemetry-proto/tree/v1.9.0/examples)):
- **IDs**: Hex-encoded strings (traceId: 32 chars, spanId: 16 chars)
- **Timestamps**: String-encoded uint64 nanoseconds (to avoid precision loss)
- **Enums**: Integer values (not string names)
- **Attributes**: Array of objects with typed values (stringValue, intValue, etc.)
- **Resource attributes**: Stored in Kafka record headers (not in JSON body)

#### Trace Format (Single Span)

```json
{
  "name": "HTTP GET /api/users",
  "traceId": "4bf92f3577b34da6a3ce929d0e0e4736",
  "spanId": "00f067aa0ba902b7",
  "kind": 2,
  "startTimeUnixNano": "1544712660300000000",
  "endTimeUnixNano": "1544712660600000000",
  "attributes": [
    {
      "key": "http.method",
      "value": {"stringValue": "GET"}
    },
    {
      "key": "http.status_code",
      "value": {"intValue": "200"}
    }
  ],
  "status": {
    "code": 1
  }
}
```

**Kafka Headers** (resource attributes):
```
service.name: my-service
service.version: 1.0.0
deployment.environment: production
```

#### Metric Format (Single Metric)

```json
{
  "name": "http.server.request_count",
  "description": "Total HTTP requests",
  "unit": "{requests}",
  "type": "sum",
  "aggregationTemporality": 2,
  "isMonotonic": true,
  "dataPoints": [
    {
      "attributes": [
        {
          "key": "http.route",
          "value": {"stringValue": "/api/users"}
        },
        {
          "key": "http.status_code",
          "value": {"intValue": "200"}
        }
      ],
      "startTimeUnixNano": "1544712660000000000",
      "timeUnixNano": "1544712660300000000",
      "value": 42
    }
  ]
}
```

**Kafka Headers** (resource attributes):
```
service.name: my-service
service.version: 1.0.0
```

#### Log Format (Single Log Record)

```json
{
  "timeUnixNano": "1544712660300000000",
  "observedTimeUnixNano": "1544712660300100000",
  "severityNumber": 9,
  "severityText": "INFO",
  "body": {
    "stringValue": "User login successful"
  },
  "attributes": [
    {
      "key": "user.id",
      "value": {"stringValue": "user123"}
    },
    {
      "key": "login.attempts",
      "value": {"intValue": "1"}
    }
  ],
  "traceId": "4bf92f3577b34da6a3ce929d0e0e4736",
  "spanId": "00f067aa0ba902b7",
  "flags": 1
}
```

**Kafka Headers** (resource attributes):
```
service.name: my-service
service.version: 1.0.0
```

### Protobuf Format

When using `SerializationFormatProtobuf`, data is serialized using the official [OpenTelemetry Protocol (OTLP)](https://github.com/open-telemetry/opentelemetry-proto) protobuf definitions:

- **Traces**: `opentelemetry.proto.trace.v1.ResourceSpans` (one span per record)
- **Metrics**: `opentelemetry.proto.metrics.v1.ResourceMetrics` (one metric per record)
- **Logs**: `opentelemetry.proto.logs.v1.ResourceLogs` (one log per record)

The protobuf format follows the exact same structure as defined in the OTLP specification, making it compatible with any OTLP-compliant consumer. **Note**: In protobuf format, resource attributes are included in the message body (not in Kafka headers) to maintain full OTLP compliance.

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

Add resource attributes to identify your service:

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

exporter, err := exporter.NewTraceExporter(
    exporter.WithBrokers("localhost:9092"),
    exporter.WithResource(res),
)
```

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
| `WithResource(resource *resource.Resource)` | OpenTelemetry resource | `nil` |
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

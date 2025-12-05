package redpandaotelexporter

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"slices"

	"github.com/twmb/franz-go/pkg/kgo"
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
	p := kgo.BasicConsistentPartitioner(func(topic string) func(r *kgo.Record, n int) int {
		// Would use murmur2, but I don't want to duplicate a bunch of code from franz
		fnv32a := func(b []byte) uint32 {
			h := fnv.New32a()
			h.Write(b)
			return h.Sum32()
		}
		h := kgo.KafkaHasher(fnv32a)
		p := kgo.StickyKeyPartitioner(h).ForTopic(topic)
		return func(r *kgo.Record, n int) int {
			idx := bytes.IndexByte(r.Key, '/')
			if idx < 0 {
				return p.Partition(r, n)
			}
			return h(r.Key[0:idx], n)
		}
	})
	// Allow the user to override the partitioner if they want
	opts = slices.Concat([]Option{WithKafkaOptions(kgo.RecordPartitioner(p))}, opts)
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
	// Use trace ID/span ID as the key, the partitioner we use only partition based on the trace
	signals := make([]signalRecord, len(spans))
	for i, span := range spans {
		key := fmt.Appendf(nil, "%s/%s", span.SpanContext().TraceID().String(), span.SpanContext().SpanID().String())
		signals[i] = signalRecord{
			key:     key,
			payload: spanToProto(span),
		}
	}
	return e.export(
		ctx,
		signals,
		otlpTraceProtoSchema,
		otlpTraceJSONSchema,
	)
}

// Shutdown shuts down the exporter.
func (e *TraceExporter) Shutdown(ctx context.Context) error {
	return e.Exporter.Shutdown(ctx)
}

// ForceFlush flushes any pending spans to Kafka.
func (e *TraceExporter) ForceFlush(ctx context.Context) error {
	return e.client.Flush(ctx)
}

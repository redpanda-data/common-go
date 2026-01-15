// Copyright 2026 Redpanda Data, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otlpfile

import (
	"context"
	"io"
	"os"
	"sync"
	"sync/atomic"
	_ "unsafe" // Required for go:linkname.

	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	_ "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"       // imported to ensure internal/transform.ResourceLogs get's included in our binary.
	_ "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp" // imported to ensure internal/transform.ResourceMetrics get's included in our binary.
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

// File represents a file-backed OTLP sink. It provides exporters for traces,
// logs, and metrics that write OTLP JSON-lines to the named file.
type File struct {
	Path string
	file *syncWriter
}

// Open opens or creates an OTLP JSON-lines file at the given path and returns
// a [File] that can be used to create exporters writing to that file.
func Open(path string) (*File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o664) //nolint:gosec // This is a log file, we want everyone to have permissions.
	if err != nil {
		return nil, err
	}
	return &File{
		Path: path,
		file: &syncWriter{WriteCloser: file},
	}, nil
}

// Close closes the underlying file writer backing the File.
func (f *File) Close() error {
	return f.file.Close()
}

// SpanExporter returns an OTLP span exporter that writes spans to this file.
func (f *File) SpanExporter() sdktrace.SpanExporter {
	exporter, err := otlptrace.New(context.Background(), NewClient(f.file.AddWriter()))
	if err != nil {
		panic(err) // Should never happen, the error returned is from .Start which we know will return nil.
	}
	return exporter
}

// LogExporter returns an OTLP log exporter that writes logs to this file.
func (f *File) LogExporter() sdklog.Exporter {
	return NewLogExporter(f.file.AddWriter())
}

// MetricExporter returns an OTLP metric exporter that writes metrics to this file.
func (f *File) MetricExporter() sdkmetric.Exporter {
	return NewMetricExporter(f.file.AddWriter())
}

// Client implements otlptrace.Client and serializes traces to the provided writer
// in OTLP JSON-lines format.
type Client struct {
	writer       io.WriteCloser
	jsonEncoder  ptrace.Marshaler
	protoDecoder ptrace.Unmarshaler
}

// NewClient returns a Client that writes OTLP traces to the provided writer.
func NewClient(writer io.WriteCloser) *Client {
	return &Client{
		writer:       writer,
		jsonEncoder:  &ptrace.JSONMarshaler{},
		protoDecoder: &ptrace.ProtoUnmarshaler{},
	}
}

var _ otlptrace.Client = &Client{}

// Start implements the tracing interface but does nothing.
func (*Client) Start(_ context.Context) error { return nil }

// Stop closes the underlying span writer.
func (c *Client) Stop(_ context.Context) error { return c.writer.Close() }

// UploadTraces implements the tracing interface and writes the given spans to disk
func (c *Client) UploadTraces(_ context.Context, spans []*tracepb.ResourceSpans) error {
	// The OTLP file format is incompatible with the default JSON encoding of
	// the tracepb package:
	// - OTLP == camelCase vs tracepb == snake_case
	// - OTLP == bytes as hex vs tracepb == bytes as base64
	// https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding

	// The easiest way to serialize correctly is through the otel collector
	// package's which don't expose a mutable interface.
	// Therefore the only way to actually serialize traces correctly is to:
	// go -> proto -> ptrace -> json
	// In theory an otlp file exporter is on the way but the standard isn't
	// official yet so it'll be a while before there's an upstream package.
	// TODO: It might be possible to avoid doing this dance with linkname as
	// well.

	protobytes, err := proto.Marshal(&tracepb.TracesData{ResourceSpans: spans})
	if err != nil {
		return err
	}

	traces, err := c.protoDecoder.UnmarshalTraces(protobytes)
	if err != nil {
		return err
	}

	data, err := c.jsonEncoder.MarshalTraces(traces)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	n, err := c.writer.Write(data)
	if err != nil {
		return err
	}

	if n != len(data) {
		return io.ErrShortWrite
	}

	return nil
}

// LogExporter writes logs to an io.WriteCloser in OTLP JSON-lines format.
type LogExporter struct {
	writer       io.WriteCloser
	jsonEncoder  plog.Marshaler
	protoDecoder plog.Unmarshaler
}

// NewLogExporter returns a LogExporter that writes OTLP logs to writer.
func NewLogExporter(writer io.WriteCloser) *LogExporter {
	return &LogExporter{
		writer:       writer,
		jsonEncoder:  &plog.JSONMarshaler{},
		protoDecoder: &plog.ProtoUnmarshaler{},
	}
}

var _ sdklog.Exporter = &LogExporter{}

// ForceFlush is a no-op for file-backed exporters.
func (*LogExporter) ForceFlush(_ context.Context) error { return nil }

// Shutdown closes the underlying writer.
func (e *LogExporter) Shutdown(_ context.Context) error { return e.writer.Close() }

// Export writes the provided log records to the underlying writer encoded as
// OTLP JSON lines.
func (e *LogExporter) Export(_ context.Context, records []sdklog.Record) error {
	resourceLogs := transformResourceLogs(records)

	protobytes, err := proto.Marshal(&logspb.LogsData{ResourceLogs: resourceLogs})
	if err != nil {
		return err
	}

	traces, err := e.protoDecoder.UnmarshalLogs(protobytes)
	if err != nil {
		return err
	}

	data, err := e.jsonEncoder.MarshalLogs(traces)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	n, err := e.writer.Write(data)
	if err != nil {
		return err
	}

	if n != len(data) {
		return io.ErrShortWrite
	}

	return nil
}

// transformResourceLogs translates log.Records into their protobuf
// counterparts.
//
// As seems to be a recurring case for OTLP, the useful parts of the SDK are
// hidden away in an internal package. Rather than copying in a function that's
// already compiled in or making a buggy reimplementation, we step around go's
// internal package limitations by using linkname. Don't try this at home.
// https://github.com/open-telemetry/opentelemetry-go/blob/d4a557c53d59e9cbdf93099eea4bac97c3130487/exporters/otlp/otlplog/otlploghttp/internal/transform/log.go#L25C6-L25C18
//
//go:linkname transformResourceLogs go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp/internal/transform.ResourceLogs
func transformResourceLogs(records []sdklog.Record) []*logspb.ResourceLogs

// syncWriter is a syncronized [io.WriteCloser] which only closes the
// underlying implementation once all writers have call closed.
type syncWriter struct {
	io.WriteCloser

	mu      sync.Mutex
	writers int32
}

func (w *syncWriter) AddWriter() *syncWriter {
	atomic.AddInt32(&w.writers, 1)
	return w
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.WriteCloser.Write(p)
}

func (w *syncWriter) Close() error {
	if atomic.AddInt32(&w.writers, -1) > 0 {
		return nil
	}
	return w.WriteCloser.Close()
}

// MetricExporter writes metrics to an io.WriteCloser in OTLP JSON-lines format.
type MetricExporter struct {
	writer       io.WriteCloser
	jsonEncoder  pmetric.Marshaler
	protoDecoder pmetric.Unmarshaler
	temporality  sdkmetric.TemporalitySelector
	aggregation  sdkmetric.AggregationSelector
}

// NewMetricExporter returns a MetricExporter that writes OTLP metrics to the
// provided writer.
func NewMetricExporter(writer io.WriteCloser) *MetricExporter {
	return &MetricExporter{
		writer:       writer,
		temporality:  sdkmetric.DefaultTemporalitySelector,
		aggregation:  sdkmetric.DefaultAggregationSelector,
		jsonEncoder:  &pmetric.JSONMarshaler{},
		protoDecoder: &pmetric.ProtoUnmarshaler{},
	}
}

var _ sdkmetric.Exporter = &MetricExporter{}

// Temporality returns the temporality selection for the instrument kind.
func (e *MetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.temporality(k)
}

// Aggregation returns the aggregation selection for the instrument kind.
func (e *MetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.aggregation(k)
}

// ForceFlush is a no-op for file-backed metric exporters.
func (*MetricExporter) ForceFlush(_ context.Context) error { return nil }

// Shutdown closes the underlying writer.
func (e *MetricExporter) Shutdown(_ context.Context) error { return e.writer.Close() }

// Export writes the provided ResourceMetrics to the underlying writer encoded
// as OTLP JSON lines.
func (e *MetricExporter) Export(_ context.Context, metrics *metricdata.ResourceMetrics) error {
	resourceMetrics, err := transformResourceMetrics(metrics)
	if err != nil {
		return err
	}

	protobytes, err := proto.Marshal(&metricspb.MetricsData{ResourceMetrics: []*metricspb.ResourceMetrics{resourceMetrics}})
	if err != nil {
		return err
	}

	traces, err := e.protoDecoder.UnmarshalMetrics(protobytes)
	if err != nil {
		return err
	}

	data, err := e.jsonEncoder.MarshalMetrics(traces)
	if err != nil {
		return err
	}

	data = append(data, '\n')

	n, err := e.writer.Write(data)
	if err != nil {
		return err
	}

	if n != len(data) {
		return io.ErrShortWrite
	}

	return nil
}

//go:linkname transformResourceMetrics go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp/internal/transform.ResourceMetrics
func transformResourceMetrics(rm *metricdata.ResourceMetrics) (*metricspb.ResourceMetrics, error)

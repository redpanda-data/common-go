// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package redpandaotelexporter_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	exporter "github.com/redpanda-data/common-go/redpanda-otel-exporter"
)

// Example demonstrates how to use the Redpanda OpenTelemetry exporters
// for traces, metrics, and logs.
func Example() {
	ctx := context.Background()

	// Create resource with service information
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("example-service"),
			semconv.ServiceVersion("1.0.0"),
			attribute.String("environment", "development"),
		),
	)
	if err != nil {
		log.Printf("failed to create resource: %v", err)
		return
	}

	// Configure Kafka brokers and topics
	brokers := []string{"localhost:9092"}

	// Choose serialization format:
	// - SerializationFormatJSON (default): Human-readable JSON format
	// - SerializationFormatProtobuf: Efficient binary OTLP protobuf format
	serializationFormat := exporter.SerializationFormatJSON

	// Initialize trace exporter
	traceExporter, err := exporter.NewTraceExporter(
		exporter.WithBrokers(brokers...),
		exporter.WithSerializationFormat(serializationFormat),
	)
	if err != nil {
		log.Printf("failed to create trace exporter: %v", err)
		return
	}
	defer func() {
		if err := traceExporter.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown trace exporter: %v", err)
		}
	}()

	// Initialize trace provider
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	defer func() {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown tracer provider: %v", err)
		}
	}()
	otel.SetTracerProvider(tracerProvider)

	// Initialize metric exporter
	metricExporter, err := exporter.NewMetricExporter(
		exporter.WithBrokers(brokers...),
		exporter.WithSerializationFormat(serializationFormat),
	)
	if err != nil {
		log.Printf("failed to create metric exporter: %v", err)
		return
	}
	defer func() {
		if err := metricExporter.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown metric exporter: %v", err)
		}
	}()

	// Initialize metric provider
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res),
	)
	defer func() {
		if err := meterProvider.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown meter provider: %v", err)
		}
	}()
	otel.SetMeterProvider(meterProvider)

	// Initialize log exporter
	logExporter, err := exporter.NewLogExporter(
		exporter.WithBrokers(brokers...),
		exporter.WithSerializationFormat(serializationFormat),
	)
	if err != nil {
		log.Printf("failed to create log exporter: %v", err)
		return
	}
	defer func() {
		if err := logExporter.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown log exporter: %v", err)
		}
	}()

	// Initialize log provider
	loggerProvider := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	defer func() {
		if err := loggerProvider.Shutdown(ctx); err != nil {
			log.Printf("failed to shutdown logger provider: %v", err)
		}
	}()

	// Example: Create and export traces
	fmt.Println("Creating traces...")
	tracer := otel.Tracer("example-tracer")
	ctx, span := tracer.Start(ctx, "example-operation",
		trace.WithAttributes(
			attribute.String("operation.type", "example"),
			attribute.Int("operation.count", 42),
		),
	)
	span.AddEvent("processing started")
	time.Sleep(100 * time.Millisecond)
	span.AddEvent("processing completed")
	span.End()

	// Example: Create and export metrics
	fmt.Println("Creating metrics...")
	meter := otel.Meter("example-meter")
	counter, err := meter.Int64Counter(
		"example.counter",
		metric.WithDescription("An example counter"),
		metric.WithUnit("{operations}"),
	)
	if err != nil {
		log.Printf("failed to create counter: %v", err)
		return
	}
	counter.Add(ctx, 1, metric.WithAttributes(
		attribute.String("key", "value"),
	))

	histogram, err := meter.Float64Histogram(
		"example.histogram",
		metric.WithDescription("An example histogram"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		log.Printf("failed to create histogram: %v", err)
		return
	}
	histogram.Record(ctx, 123.45, metric.WithAttributes(
		attribute.String("method", "GET"),
	))

	// Example: Create and export logs
	fmt.Println("Creating logs...")
	logger := loggerProvider.Logger("example-logger")
	logRecord := otellog.Record{}
	logRecord.SetTimestamp(time.Now())
	logRecord.SetBody(otellog.StringValue("This is an example log message"))
	logRecord.SetSeverity(otellog.SeverityInfo)
	logRecord.SetSeverityText("INFO")
	logRecord.AddAttributes(
		otellog.String("log.key", "log.value"),
		otellog.Int("log.count", 100),
	)
	logger.Emit(ctx, logRecord)

	fmt.Println("Waiting for data to be exported...")
	time.Sleep(15 * time.Second)

	fmt.Println("Done! Check your Kafka topics for the exported data:")
	fmt.Println("  - otel-traces")
	fmt.Println("  - otel-metrics")
	fmt.Println("  - otel-logs")
}

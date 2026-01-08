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

package redpandaotelexporter

import (
	"context"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// MetricExporter implements the OpenTelemetry metric exporter interface.
type MetricExporter struct {
	*Exporter
}

// Ensure MetricExporter implements the Exporter interface
var _ metric.Exporter = (*MetricExporter)(nil)

// NewMetricExporter creates a new metric exporter that writes to Kafka.
// By default, metrics are written to the "otel-metrics" topic, which can be overridden using WithTopic.
//
// Required: WithBrokers option must be provided.
//
// Example:
//
//	exporter, err := NewMetricExporter(
//	    WithBrokers("localhost:9092"),
//	    WithResource(res),
//	)
//
// To use a custom topic:
//
//	exporter, err := NewMetricExporter(
//	    WithBrokers("localhost:9092"),
//	    WithTopic("custom-metrics"),
//	)
func NewMetricExporter(opts ...Option) (*MetricExporter, error) {
	exporter, err := newExporter("otel-metrics", opts...)
	if err != nil {
		return nil, err
	}

	return &MetricExporter{
		Exporter: exporter,
	}, nil
}

// Export exports a batch of metrics to Kafka.
func (e *MetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if rm == nil {
		return nil
	}
	// Use trace ID/span ID as the key, the partitioner we use only partition based on the trace
	var signals []signalRecord
	for _, scope := range rm.ScopeMetrics {
		for _, metric := range scope.Metrics {
			signals = append(signals, signalRecord{
				key:     []byte(metric.Name),
				payload: metricToProto(metric, scope.Scope, rm.Resource),
			})
		}
	}
	return e.export(
		ctx,
		signals,
		otlpMetricProtoSchema,
		otlpMetricJSONSchema,
	)
}

// Temporality returns the Temporality to use for an instrument kind.
func (*MetricExporter) Temporality(kind metric.InstrumentKind) metricdata.Temporality {
	return metric.DefaultTemporalitySelector(kind)
}

// Aggregation returns the Aggregation to use for an instrument kind.
func (*MetricExporter) Aggregation(kind metric.InstrumentKind) metric.Aggregation {
	return metric.DefaultAggregationSelector(kind)
}

// ForceFlush flushes any pending metrics.
func (e *MetricExporter) ForceFlush(ctx context.Context) error {
	return e.client.Flush(ctx)
}

// Shutdown shuts down the exporter.
func (e *MetricExporter) Shutdown(ctx context.Context) error {
	return e.Exporter.Shutdown(ctx)
}

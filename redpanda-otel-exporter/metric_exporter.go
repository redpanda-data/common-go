package redpandaotelexporter

import (
	"context"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/sdk/instrumentation"
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

	if e.config.serializationFormat == SerializationFormatProtobuf {
		return e.exportMetricsProtobuf(ctx, rm)
	}
	return e.exportMetricsJSON(ctx, rm)
}

// exportMetricsJSON exports metrics in JSON format
func (e *MetricExporter) exportMetricsJSON(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	records := make([]*kgo.Record, 0)

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metricData := e.metricToMap(m, sm.Scope)

			value, err := marshalJSON(metricData)
			if err != nil {
				return fmt.Errorf("failed to marshal metric: %w", err)
			}

			// Use metric name as key for partitioning
			key := []byte(m.Name)

			record := &kgo.Record{
				Topic: e.config.topic,
				Key:   key,
				Value: value,
			}

			// Add resource attributes as Kafka headers instead of in the JSON body
			if rm.Resource != nil {
				record.Headers = resourceToHeaders(rm.Resource)
			}

			records = append(records, record)
		}
	}

	if len(records) == 0 {
		return nil
	}

	return e.produceBatch(ctx, records)
}

// exportMetricsProtobuf exports metrics in OTLP protobuf format
// Each metric is written as an individual record with resource attributes in headers
func (e *MetricExporter) exportMetricsProtobuf(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if rm == nil {
		return nil
	}

	records := make([]*kgo.Record, 0)

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			// Convert metric to protobuf
			metricProto := metricToProto(m)

			value, err := marshalProtobuf(metricProto)
			if err != nil {
				return fmt.Errorf("failed to marshal metric to protobuf: %w", err)
			}

			// Use metric name as the key for partitioning (same as JSON format)
			key := []byte(m.Name)

			record := &kgo.Record{
				Topic: e.config.topic,
				Key:   key,
				Value: value,
			}

			// Add resource attributes as Kafka headers (same as JSON format)
			if rm.Resource != nil {
				record.Headers = resourceToHeaders(rm.Resource)
			}

			records = append(records, record)
		}
	}

	if len(records) == 0 {
		return nil
	}

	return e.produceBatch(ctx, records)
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

// metricToMap converts a metric to a map for JSON serialization
func (e *MetricExporter) metricToMap(m metricdata.Metrics, scope instrumentation.Scope) map[string]any {
	result := map[string]any{
		"name":        m.Name,
		"description": m.Description,
		"unit":        m.Unit,
	}

	// Add scope information
	if scope.Name != "" {
		scopeMap := map[string]any{
			"name": scope.Name,
		}
		if scope.Version != "" {
			scopeMap["version"] = scope.Version
		}
		if scope.SchemaURL != "" {
			scopeMap["schemaUrl"] = scope.SchemaURL
		}
		result["scope"] = scopeMap
	}

	// Add data based on type
	switch data := m.Data.(type) {
	case metricdata.Gauge[int64]:
		result["type"] = "gauge"
		result["dataPoints"] = numberDataPointsToMaps(data.DataPoints)
	case metricdata.Gauge[float64]:
		result["type"] = "gauge"
		result["dataPoints"] = numberDataPointsToMaps(data.DataPoints)
	case metricdata.Sum[int64]:
		result["type"] = "sum"
		result["aggregationTemporality"] = temporalityToInt(data.Temporality)
		result["isMonotonic"] = data.IsMonotonic
		result["dataPoints"] = numberDataPointsToMaps(data.DataPoints)
	case metricdata.Sum[float64]:
		result["type"] = "sum"
		result["aggregationTemporality"] = temporalityToInt(data.Temporality)
		result["isMonotonic"] = data.IsMonotonic
		result["dataPoints"] = numberDataPointsToMaps(data.DataPoints)
	case metricdata.Histogram[int64]:
		result["type"] = "histogram"
		result["aggregationTemporality"] = temporalityToInt(data.Temporality)
		result["dataPoints"] = histogramDataPointsToMaps(data.DataPoints)
	case metricdata.Histogram[float64]:
		result["type"] = "histogram"
		result["aggregationTemporality"] = temporalityToInt(data.Temporality)
		result["dataPoints"] = histogramDataPointsToMaps(data.DataPoints)
	case metricdata.ExponentialHistogram[int64]:
		result["type"] = "exponentialHistogram"
		result["aggregationTemporality"] = temporalityToInt(data.Temporality)
		result["dataPoints"] = exponentialHistogramDataPointsToMaps(data.DataPoints)
	case metricdata.ExponentialHistogram[float64]:
		result["type"] = "exponentialHistogram"
		result["aggregationTemporality"] = temporalityToInt(data.Temporality)
		result["dataPoints"] = exponentialHistogramDataPointsToMaps(data.DataPoints)
	case metricdata.Summary:
		result["type"] = "summary"
		result["dataPoints"] = e.summaryDataPointsToMaps(data.DataPoints)
	}

	return result
}

// numberDataPointsToMaps converts number data points to maps
func numberDataPointsToMaps[N int64 | float64](points []metricdata.DataPoint[N]) []map[string]any {
	result := make([]map[string]any, len(points))
	for i, dp := range points {
		result[i] = map[string]any{
			"attributes":        attributesToArray(dp.Attributes.ToSlice()),
			"startTimeUnixNano": fmt.Sprintf("%d", dp.StartTime.UnixNano()),
			"timeUnixNano":      fmt.Sprintf("%d", dp.Time.UnixNano()),
			"value":             dp.Value,
			"exemplars":         exemplarsToMaps(dp.Exemplars),
		}
	}
	return result
}

// histogramDataPointsToMaps converts histogram data points to maps
func histogramDataPointsToMaps[N int64 | float64](points []metricdata.HistogramDataPoint[N]) []map[string]any {
	result := make([]map[string]any, len(points))
	for i, dp := range points {
		dpMap := map[string]any{
			"attributes":        attributesToArray(dp.Attributes.ToSlice()),
			"startTimeUnixNano": fmt.Sprintf("%d", dp.StartTime.UnixNano()),
			"timeUnixNano":      fmt.Sprintf("%d", dp.Time.UnixNano()),
			"count":             fmt.Sprintf("%d", dp.Count),
			"explicitBounds":    dp.Bounds,
			"bucketCounts":      convertUint64SliceToStrings(dp.BucketCounts),
			"exemplars":         exemplarsToMaps(dp.Exemplars),
			"sum":               dp.Sum,
		}
		if minVal, defined := dp.Min.Value(); defined {
			dpMap["min"] = minVal
		}
		if maxVal, defined := dp.Max.Value(); defined {
			dpMap["max"] = maxVal
		}
		result[i] = dpMap
	}
	return result
}

// exponentialHistogramDataPointsToMaps converts exponential histogram data points to maps
func exponentialHistogramDataPointsToMaps[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N]) []map[string]any {
	result := make([]map[string]any, len(points))
	for i, dp := range points {
		dpMap := map[string]any{
			"attributes":        attributesToArray(dp.Attributes.ToSlice()),
			"startTimeUnixNano": fmt.Sprintf("%d", dp.StartTime.UnixNano()),
			"timeUnixNano":      fmt.Sprintf("%d", dp.Time.UnixNano()),
			"count":             fmt.Sprintf("%d", dp.Count),
			"scale":             dp.Scale,
			"zeroCount":         fmt.Sprintf("%d", dp.ZeroCount),
			"exemplars":         exemplarsToMaps(dp.Exemplars),
			"sum":               dp.Sum,
		}
		if minVal, defined := dp.Min.Value(); defined {
			dpMap["min"] = minVal
		}
		if maxVal, defined := dp.Max.Value(); defined {
			dpMap["max"] = maxVal
		}
		if dp.ZeroThreshold != 0 {
			dpMap["zeroThreshold"] = dp.ZeroThreshold
		}

		// Add positive and negative buckets
		dpMap["positive"] = map[string]any{
			"offset":       dp.PositiveBucket.Offset,
			"bucketCounts": convertUint64SliceToStrings(dp.PositiveBucket.Counts),
		}
		dpMap["negative"] = map[string]any{
			"offset":       dp.NegativeBucket.Offset,
			"bucketCounts": convertUint64SliceToStrings(dp.NegativeBucket.Counts),
		}

		result[i] = dpMap
	}
	return result
}

// summaryDataPointsToMaps converts summary data points to maps
func (*MetricExporter) summaryDataPointsToMaps(points []metricdata.SummaryDataPoint) []map[string]any {
	result := make([]map[string]any, len(points))
	for i, dp := range points {
		quantiles := make([]map[string]any, len(dp.QuantileValues))
		for j, qv := range dp.QuantileValues {
			quantiles[j] = map[string]any{
				"quantile": qv.Quantile,
				"value":    qv.Value,
			}
		}

		result[i] = map[string]any{
			"attributes":        attributesToArray(dp.Attributes.ToSlice()),
			"startTimeUnixNano": fmt.Sprintf("%d", dp.StartTime.UnixNano()),
			"timeUnixNano":      fmt.Sprintf("%d", dp.Time.UnixNano()),
			"count":             fmt.Sprintf("%d", dp.Count),
			"sum":               dp.Sum,
			"quantileValues":    quantiles,
		}
	}
	return result
}

// exemplarsToMaps converts exemplars to maps
func exemplarsToMaps[N int64 | float64](exemplars []metricdata.Exemplar[N]) []map[string]any {
	if len(exemplars) == 0 {
		return nil
	}

	result := make([]map[string]any, len(exemplars))
	for i, ex := range exemplars {
		exMap := map[string]any{
			"timeUnixNano":       fmt.Sprintf("%d", ex.Time.UnixNano()),
			"value":              ex.Value,
			"filteredAttributes": attributesToArray(ex.FilteredAttributes),
		}
		if len(ex.TraceID) > 0 {
			exMap["traceId"] = fmt.Sprintf("%x", ex.TraceID)
		}
		if len(ex.SpanID) > 0 {
			exMap["spanId"] = fmt.Sprintf("%x", ex.SpanID)
		}
		result[i] = exMap
	}
	return result
}

// temporalityToInt converts metricdata.Temporality to OTLP integer enum
// Per OTLP spec: UNSPECIFIED=0, DELTA=1, CUMULATIVE=2
func temporalityToInt(t metricdata.Temporality) int {
	switch t {
	case metricdata.DeltaTemporality:
		return 1
	case metricdata.CumulativeTemporality:
		return 2
	default:
		return 0
	}
}

// convertUint64SliceToStrings converts a slice of uint64 to strings per OTLP spec
func convertUint64SliceToStrings(values []uint64) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = fmt.Sprintf("%d", v)
	}
	return result
}

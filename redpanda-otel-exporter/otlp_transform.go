package redpandaotelexporter

import (
	"fmt"
	"math"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// int64ToUint64 safely converts an int64 to uint64.
// For timestamps, UnixNano() returns int64 but OTLP protobuf expects uint64.
// Since Unix timestamps are always positive (time since epoch), this conversion is safe.
func int64ToUint64(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// intToInt32 safely converts an int to int32.
// For log severity numbers, values are always in the range 0-24, making this conversion safe.
func intToInt32(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}

// attributeToProto converts an attribute.KeyValue to OTLP protobuf format
func attributeToProto(attr attribute.KeyValue) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   string(attr.Key),
		Value: attributeValueToProto(attr.Value),
	}
}

// attributeValueToProto converts an attribute.Value to OTLP protobuf format
func attributeValueToProto(v attribute.Value) *commonpb.AnyValue {
	switch v.Type() {
	case attribute.BOOL:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
	case attribute.INT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v.AsInt64()}}
	case attribute.FLOAT64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}}
	case attribute.BOOLSLICE:
		slice := v.AsBoolSlice()
		values := make([]*commonpb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: item}}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.INT64SLICE:
		slice := v.AsInt64Slice()
		values := make([]*commonpb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: item}}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.FLOAT64SLICE:
		slice := v.AsFloat64Slice()
		values := make([]*commonpb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: item}}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case attribute.STRINGSLICE:
		slice := v.AsStringSlice()
		values := make([]*commonpb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: item}}
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	default:
		// Handles attribute.STRING and any other unknown types by converting to string
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.AsString()}}
	}
}

// spanToProto converts a ReadOnlySpan to OTLP protobuf format
func spanToProto(span sdktrace.ReadOnlySpan) *tracepb.Span {
	sc := span.SpanContext()

	traceID := sc.TraceID()
	spanID := sc.SpanID()

	pbSpan := &tracepb.Span{
		TraceId:           traceID[:],
		SpanId:            spanID[:],
		Name:              span.Name(),
		Kind:              spanKindToProto(span.SpanKind()),
		StartTimeUnixNano: int64ToUint64(span.StartTime().UnixNano()),
		EndTimeUnixNano:   int64ToUint64(span.EndTime().UnixNano()),
		Status:            spanStatusToProto(span.Status()),
		TraceState:        sc.TraceState().String(),
	}

	// Add parent span ID if present
	if span.Parent().IsValid() {
		parentSpanID := span.Parent().SpanID()
		pbSpan.ParentSpanId = parentSpanID[:]
	}

	// Add attributes
	attrs := span.Attributes()
	pbSpan.Attributes = make([]*commonpb.KeyValue, len(attrs))
	for i, attr := range attrs {
		pbSpan.Attributes[i] = attributeToProto(attr)
	}

	// Add events
	events := span.Events()
	pbSpan.Events = make([]*tracepb.Span_Event, len(events))
	for i, event := range events {
		pbEvent := &tracepb.Span_Event{
			TimeUnixNano: int64ToUint64(event.Time.UnixNano()),
			Name:         event.Name,
			Attributes:   make([]*commonpb.KeyValue, len(event.Attributes)),
		}
		for j, attr := range event.Attributes {
			pbEvent.Attributes[j] = attributeToProto(attr)
		}
		pbSpan.Events[i] = pbEvent
	}

	// Add links
	links := span.Links()
	pbSpan.Links = make([]*tracepb.Span_Link, len(links))
	for i, link := range links {
		linkTraceID := link.SpanContext.TraceID()
		linkSpanID := link.SpanContext.SpanID()
		pbLink := &tracepb.Span_Link{
			TraceId:    linkTraceID[:],
			SpanId:     linkSpanID[:],
			TraceState: link.SpanContext.TraceState().String(),
			Attributes: make([]*commonpb.KeyValue, len(link.Attributes)),
		}
		for j, attr := range link.Attributes {
			pbLink.Attributes[j] = attributeToProto(attr)
		}
		pbSpan.Links[i] = pbLink
	}

	return pbSpan
}

// spanKindToProto converts span kind to protobuf format
func spanKindToProto(kind trace.SpanKind) tracepb.Span_SpanKind {
	switch kind {
	case trace.SpanKindInternal:
		return tracepb.Span_SPAN_KIND_INTERNAL
	case trace.SpanKindServer:
		return tracepb.Span_SPAN_KIND_SERVER
	case trace.SpanKindClient:
		return tracepb.Span_SPAN_KIND_CLIENT
	case trace.SpanKindProducer:
		return tracepb.Span_SPAN_KIND_PRODUCER
	case trace.SpanKindConsumer:
		return tracepb.Span_SPAN_KIND_CONSUMER
	default:
		return tracepb.Span_SPAN_KIND_UNSPECIFIED
	}
}

// spanStatusToProto converts span status to protobuf format
func spanStatusToProto(status sdktrace.Status) *tracepb.Status {
	pbStatus := &tracepb.Status{
		Message: status.Description,
	}

	switch status.Code {
	case codes.Ok:
		pbStatus.Code = tracepb.Status_STATUS_CODE_OK
	case codes.Error:
		pbStatus.Code = tracepb.Status_STATUS_CODE_ERROR
	default:
		pbStatus.Code = tracepb.Status_STATUS_CODE_UNSET
	}

	return pbStatus
}

// logRecordToProto converts a log record to OTLP protobuf format
func logRecordToProto(record sdklog.Record) *logspb.LogRecord {
	pbLog := &logspb.LogRecord{
		TimeUnixNano:         int64ToUint64(record.Timestamp().UnixNano()),
		ObservedTimeUnixNano: int64ToUint64(record.ObservedTimestamp().UnixNano()),
		SeverityNumber:       logspb.SeverityNumber(intToInt32(int(record.Severity()))),
		SeverityText:         record.SeverityText(),
		Body:                 logBodyToProto(record.Body()),
	}

	// Add trace context
	if record.TraceID().IsValid() {
		traceID := record.TraceID()
		pbLog.TraceId = traceID[:]
	}
	if record.SpanID().IsValid() {
		spanID := record.SpanID()
		pbLog.SpanId = spanID[:]
	}
	pbLog.Flags = uint32(record.TraceFlags())

	// Add attributes
	attrs := make([]*commonpb.KeyValue, 0)
	record.WalkAttributes(func(kv log.KeyValue) bool {
		attrs = append(attrs, logAttributeToProto(kv))
		return true
	})
	pbLog.Attributes = attrs

	return pbLog
}

// logBodyToProto converts log body to protobuf format
func logBodyToProto(body any) *commonpb.AnyValue {
	if body == nil {
		return nil
	}
	// For simplicity, convert to string
	return &commonpb.AnyValue{
		Value: &commonpb.AnyValue_StringValue{
			StringValue: fmt.Sprint(body),
		},
	}
}

// logAttributeToProto converts a log.KeyValue to OTLP protobuf format
func logAttributeToProto(kv log.KeyValue) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   kv.Key,
		Value: logValueToProto(kv.Value),
	}
}

// logValueToProto converts a log.Value to OTLP protobuf format
func logValueToProto(v log.Value) *commonpb.AnyValue {
	switch v.Kind() {
	case log.KindEmpty:
		return &commonpb.AnyValue{}
	case log.KindBool:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
	case log.KindFloat64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}}
	case log.KindInt64:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: v.AsInt64()}}
	case log.KindBytes:
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: v.AsBytes()}}
	case log.KindSlice:
		slice := v.AsSlice()
		values := make([]*commonpb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = logValueToProto(item)
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{ArrayValue: &commonpb.ArrayValue{Values: values}}}
	case log.KindMap:
		kvs := v.AsMap()
		pairs := make([]*commonpb.KeyValue, len(kvs))
		for i, kv := range kvs {
			pairs[i] = logAttributeToProto(kv)
		}
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_KvlistValue{KvlistValue: &commonpb.KeyValueList{Values: pairs}}}
	default:
		// Handles log.KindString and any other unknown types by converting to string
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v.AsString()}}
	}
}

// metricToProto converts an OpenTelemetry metric to OTLP protobuf format
func metricToProto(m metricdata.Metrics) *metricspb.Metric {
	pbMetric := &metricspb.Metric{
		Name:        m.Name,
		Description: m.Description,
		Unit:        m.Unit,
	}

	switch data := m.Data.(type) {
	case metricdata.Gauge[int64]:
		pbMetric.Data = &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Gauge[float64]:
		pbMetric.Data = &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Sum[int64]:
		pbMetric.Data = &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				IsMonotonic:            data.IsMonotonic,
				DataPoints:             numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Sum[float64]:
		pbMetric.Data = &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				IsMonotonic:            data.IsMonotonic,
				DataPoints:             numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Histogram[int64]:
		pbMetric.Data = &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             histogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Histogram[float64]:
		pbMetric.Data = &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             histogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.ExponentialHistogram[int64]:
		pbMetric.Data = &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             exponentialHistogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.ExponentialHistogram[float64]:
		pbMetric.Data = &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             exponentialHistogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Summary:
		pbMetric.Data = &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: summaryDataPointsToProto(data.DataPoints),
			},
		}
	}

	return pbMetric
}

// aggregationTemporalityToProto converts temporality to protobuf format
func aggregationTemporalityToProto(temporality metricdata.Temporality) metricspb.AggregationTemporality {
	switch temporality {
	case metricdata.CumulativeTemporality:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
	case metricdata.DeltaTemporality:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	default:
		return metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED
	}
}

// numberDataPointsToProto converts number data points to protobuf format
func numberDataPointsToProto[N int64 | float64](points []metricdata.DataPoint[N]) []*metricspb.NumberDataPoint {
	pbPoints := make([]*metricspb.NumberDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &metricspb.NumberDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Exemplars:         exemplarsToProto(dp.Exemplars),
		}

		// Convert attributes
		attrs := dp.Attributes.ToSlice()
		pbPoint.Attributes = make([]*commonpb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		// Set value based on type
		switch v := any(dp.Value).(type) {
		case int64:
			pbPoint.Value = &metricspb.NumberDataPoint_AsInt{AsInt: v}
		case float64:
			pbPoint.Value = &metricspb.NumberDataPoint_AsDouble{AsDouble: v}
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// histogramDataPointsToProto converts histogram data points to protobuf format
func histogramDataPointsToProto[N int64 | float64](points []metricdata.HistogramDataPoint[N]) []*metricspb.HistogramDataPoint {
	pbPoints := make([]*metricspb.HistogramDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &metricspb.HistogramDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Count:             dp.Count,
			ExplicitBounds:    dp.Bounds,
			BucketCounts:      dp.BucketCounts,
			Exemplars:         exemplarsToProto(dp.Exemplars),
		}

		// Convert sum based on type
		switch s := any(dp.Sum).(type) {
		case int64:
			pbPoint.Sum = (*float64)(nil)
			if s != 0 {
				f := float64(s)
				pbPoint.Sum = &f
			}
		case float64:
			pbPoint.Sum = &s
		}

		// Add min/max if present
		if minVal, defined := dp.Min.Value(); defined {
			switch m := any(minVal).(type) {
			case int64:
				f := float64(m)
				pbPoint.Min = &f
			case float64:
				pbPoint.Min = &m
			}
		}
		if maxVal, defined := dp.Max.Value(); defined {
			switch m := any(maxVal).(type) {
			case int64:
				f := float64(m)
				pbPoint.Max = &f
			case float64:
				pbPoint.Max = &m
			}
		}

		// Convert attributes
		attrs := dp.Attributes.ToSlice()
		pbPoint.Attributes = make([]*commonpb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// exponentialHistogramDataPointsToProto converts exponential histogram data points to protobuf format
func exponentialHistogramDataPointsToProto[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N]) []*metricspb.ExponentialHistogramDataPoint {
	pbPoints := make([]*metricspb.ExponentialHistogramDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &metricspb.ExponentialHistogramDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Count:             dp.Count,
			Scale:             dp.Scale,
			ZeroCount:         dp.ZeroCount,
			Exemplars:         exemplarsToProto(dp.Exemplars),
			Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{
				Offset:       dp.PositiveBucket.Offset,
				BucketCounts: dp.PositiveBucket.Counts,
			},
			Negative: &metricspb.ExponentialHistogramDataPoint_Buckets{
				Offset:       dp.NegativeBucket.Offset,
				BucketCounts: dp.NegativeBucket.Counts,
			},
		}

		// Convert sum based on type
		switch s := any(dp.Sum).(type) {
		case int64:
			pbPoint.Sum = (*float64)(nil)
			if s != 0 {
				f := float64(s)
				pbPoint.Sum = &f
			}
		case float64:
			pbPoint.Sum = &s
		}

		// Add min/max if present
		if minVal, defined := dp.Min.Value(); defined {
			switch m := any(minVal).(type) {
			case int64:
				f := float64(m)
				pbPoint.Min = &f
			case float64:
				pbPoint.Min = &m
			}
		}
		if maxVal, defined := dp.Max.Value(); defined {
			switch m := any(maxVal).(type) {
			case int64:
				f := float64(m)
				pbPoint.Max = &f
			case float64:
				pbPoint.Max = &m
			}
		}

		if dp.ZeroThreshold != 0 {
			pbPoint.ZeroThreshold = dp.ZeroThreshold
		}

		// Convert attributes
		attrs := dp.Attributes.ToSlice()
		pbPoint.Attributes = make([]*commonpb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// summaryDataPointsToProto converts summary data points to protobuf format
func summaryDataPointsToProto(points []metricdata.SummaryDataPoint) []*metricspb.SummaryDataPoint {
	pbPoints := make([]*metricspb.SummaryDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &metricspb.SummaryDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Count:             dp.Count,
			Sum:               dp.Sum,
		}

		// Convert quantile values
		pbPoint.QuantileValues = make([]*metricspb.SummaryDataPoint_ValueAtQuantile, len(dp.QuantileValues))
		for j, qv := range dp.QuantileValues {
			pbPoint.QuantileValues[j] = &metricspb.SummaryDataPoint_ValueAtQuantile{
				Quantile: qv.Quantile,
				Value:    qv.Value,
			}
		}

		// Convert attributes
		attrs := dp.Attributes.ToSlice()
		pbPoint.Attributes = make([]*commonpb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// exemplarsToProto converts exemplars to protobuf format
func exemplarsToProto[N int64 | float64](exemplars []metricdata.Exemplar[N]) []*metricspb.Exemplar {
	if len(exemplars) == 0 {
		return nil
	}

	pbExemplars := make([]*metricspb.Exemplar, len(exemplars))
	for i, ex := range exemplars {
		pbEx := &metricspb.Exemplar{
			TimeUnixNano: int64ToUint64(ex.Time.UnixNano()),
		}

		// Set value based on type
		switch v := any(ex.Value).(type) {
		case int64:
			pbEx.Value = &metricspb.Exemplar_AsInt{AsInt: v}
		case float64:
			pbEx.Value = &metricspb.Exemplar_AsDouble{AsDouble: v}
		}

		// Add trace context if present
		if len(ex.TraceID) > 0 {
			pbEx.TraceId = ex.TraceID
		}
		if len(ex.SpanID) > 0 {
			pbEx.SpanId = ex.SpanID
		}

		// Convert filtered attributes
		pbEx.FilteredAttributes = make([]*commonpb.KeyValue, len(ex.FilteredAttributes))
		for j, attr := range ex.FilteredAttributes {
			pbEx.FilteredAttributes[j] = attributeToProto(attr)
		}

		pbExemplars[i] = pbEx
	}
	return pbExemplars
}

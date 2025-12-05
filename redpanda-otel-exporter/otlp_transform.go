package redpandaotelexporter

import (
	"fmt"
	"math"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	pb "github.com/redpanda-data/common-go/redpanda-otel-exporter/proto"
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
func attributesToProto(attr []attribute.KeyValue) []*pb.KeyValue {
	protos := make([]*pb.KeyValue, len(attr))
	for i, a := range attr {
		protos[i] = attributeToProto(a)
	}
	return protos
}

// attributeToProto converts an attribute.KeyValue to OTLP protobuf format
func attributeToProto(attr attribute.KeyValue) *pb.KeyValue {
	return &pb.KeyValue{
		Key:   string(attr.Key),
		Value: attributeValueToProto(attr.Value),
	}
}

// attributeValueToProto converts an attribute.Value to OTLP protobuf format
func attributeValueToProto(v attribute.Value) *pb.AnyValue {
	switch v.Type() {
	case attribute.BOOL:
		return &pb.AnyValue{Value: &pb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
	case attribute.INT64:
		return &pb.AnyValue{Value: &pb.AnyValue_IntValue{IntValue: v.AsInt64()}}
	case attribute.FLOAT64:
		return &pb.AnyValue{Value: &pb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}}
	case attribute.BOOLSLICE:
		slice := v.AsBoolSlice()
		values := make([]*pb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &pb.AnyValue{Value: &pb.AnyValue_BoolValue{BoolValue: item}}
		}
		return &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{Values: values}}}
	case attribute.INT64SLICE:
		slice := v.AsInt64Slice()
		values := make([]*pb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &pb.AnyValue{Value: &pb.AnyValue_IntValue{IntValue: item}}
		}
		return &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{Values: values}}}
	case attribute.FLOAT64SLICE:
		slice := v.AsFloat64Slice()
		values := make([]*pb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &pb.AnyValue{Value: &pb.AnyValue_DoubleValue{DoubleValue: item}}
		}
		return &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{Values: values}}}
	case attribute.STRINGSLICE:
		slice := v.AsStringSlice()
		values := make([]*pb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = &pb.AnyValue{Value: &pb.AnyValue_StringValue{StringValue: item}}
		}
		return &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{Values: values}}}
	default:
		// Handles attribute.STRING and any other unknown types by converting to string
		return &pb.AnyValue{Value: &pb.AnyValue_StringValue{StringValue: v.AsString()}}
	}
}

func resourceToProto(r *resource.Resource) *pb.Resource {
	if r == nil {
		return nil
	}
	return &pb.Resource{
		Attributes: attributesToProto(r.Attributes()),
	}
}

func instrumentationScopeToProto(s instrumentation.Scope) *pb.InstrumentationScope {
	return &pb.InstrumentationScope{
		Name:       s.Name,
		Version:    s.Version,
		Attributes: attributesToProto(s.Attributes.ToSlice()),
	}
}

// spanToProto converts a ReadOnlySpan to OTLP protobuf format
func spanToProto(span sdktrace.ReadOnlySpan) *pb.Span {
	sc := span.SpanContext()

	traceID := sc.TraceID()
	spanID := sc.SpanID()

	pbSpan := &pb.Span{
		Resource:          resourceToProto(span.Resource()),
		ResourceSchemaUrl: span.Resource().SchemaURL(),
		Scope:             instrumentationScopeToProto(span.InstrumentationScope()),
		ScopeSchemaUrl:    span.InstrumentationScope().SchemaURL,
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
	pbSpan.Attributes = make([]*pb.KeyValue, len(attrs))
	for i, attr := range attrs {
		pbSpan.Attributes[i] = attributeToProto(attr)
	}

	// Add events
	events := span.Events()
	pbSpan.Events = make([]*pb.Span_Event, len(events))
	for i, event := range events {
		pbEvent := &pb.Span_Event{
			TimeUnixNano: int64ToUint64(event.Time.UnixNano()),
			Name:         event.Name,
			Attributes:   make([]*pb.KeyValue, len(event.Attributes)),
		}
		for j, attr := range event.Attributes {
			pbEvent.Attributes[j] = attributeToProto(attr)
		}
		pbSpan.Events[i] = pbEvent
	}

	// Add links
	links := span.Links()
	pbSpan.Links = make([]*pb.Span_Link, len(links))
	for i, link := range links {
		linkTraceID := link.SpanContext.TraceID()
		linkSpanID := link.SpanContext.SpanID()
		pbLink := &pb.Span_Link{
			TraceId:    linkTraceID[:],
			SpanId:     linkSpanID[:],
			TraceState: link.SpanContext.TraceState().String(),
			Attributes: make([]*pb.KeyValue, len(link.Attributes)),
		}
		for j, attr := range link.Attributes {
			pbLink.Attributes[j] = attributeToProto(attr)
		}
		pbSpan.Links[i] = pbLink
	}

	return pbSpan
}

// spanKindToProto converts span kind to protobuf format
func spanKindToProto(kind trace.SpanKind) pb.Span_SpanKind {
	switch kind {
	case trace.SpanKindInternal:
		return pb.Span_SPAN_KIND_INTERNAL
	case trace.SpanKindServer:
		return pb.Span_SPAN_KIND_SERVER
	case trace.SpanKindClient:
		return pb.Span_SPAN_KIND_CLIENT
	case trace.SpanKindProducer:
		return pb.Span_SPAN_KIND_PRODUCER
	case trace.SpanKindConsumer:
		return pb.Span_SPAN_KIND_CONSUMER
	default:
		return pb.Span_SPAN_KIND_UNSPECIFIED
	}
}

// spanStatusToProto converts span status to protobuf format
func spanStatusToProto(status sdktrace.Status) *pb.Status {
	pbStatus := &pb.Status{
		Message: status.Description,
	}

	switch status.Code {
	case codes.Ok:
		pbStatus.Code = pb.Status_STATUS_CODE_OK
	case codes.Error:
		pbStatus.Code = pb.Status_STATUS_CODE_ERROR
	default:
		pbStatus.Code = pb.Status_STATUS_CODE_UNSET
	}

	return pbStatus
}

// logRecordToProto converts a log record to OTLP protobuf format
func logRecordToProto(record sdklog.Record) *pb.LogRecord {
	pbLog := &pb.LogRecord{
		Resource:             resourceToProto(record.Resource()),
		ResourceSchemaUrl:    record.Resource().SchemaURL(),
		Scope:                instrumentationScopeToProto(record.InstrumentationScope()),
		ScopeSchemaUrl:       record.InstrumentationScope().SchemaURL,
		TimeUnixNano:         int64ToUint64(record.Timestamp().UnixNano()),
		ObservedTimeUnixNano: int64ToUint64(record.ObservedTimestamp().UnixNano()),
		SeverityNumber:       pb.SeverityNumber(intToInt32(int(record.Severity()))),
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
	attrs := make([]*pb.KeyValue, 0)
	record.WalkAttributes(func(kv log.KeyValue) bool {
		attrs = append(attrs, logAttributeToProto(kv))
		return true
	})
	pbLog.Attributes = attrs

	return pbLog
}

// logBodyToProto converts log body to protobuf format
func logBodyToProto(body any) *pb.AnyValue {
	if body == nil {
		return nil
	}
	// For simplicity, convert to string
	return &pb.AnyValue{
		Value: &pb.AnyValue_StringValue{
			StringValue: fmt.Sprint(body),
		},
	}
}

// logAttributeToProto converts a log.KeyValue to OTLP protobuf format
func logAttributeToProto(kv log.KeyValue) *pb.KeyValue {
	return &pb.KeyValue{
		Key:   kv.Key,
		Value: logValueToProto(kv.Value),
	}
}

// logValueToProto converts a log.Value to OTLP protobuf format
func logValueToProto(v log.Value) *pb.AnyValue {
	switch v.Kind() {
	case log.KindEmpty:
		return &pb.AnyValue{}
	case log.KindBool:
		return &pb.AnyValue{Value: &pb.AnyValue_BoolValue{BoolValue: v.AsBool()}}
	case log.KindFloat64:
		return &pb.AnyValue{Value: &pb.AnyValue_DoubleValue{DoubleValue: v.AsFloat64()}}
	case log.KindInt64:
		return &pb.AnyValue{Value: &pb.AnyValue_IntValue{IntValue: v.AsInt64()}}
	case log.KindBytes:
		return &pb.AnyValue{Value: &pb.AnyValue_BytesValue{BytesValue: v.AsBytes()}}
	case log.KindSlice:
		slice := v.AsSlice()
		values := make([]*pb.AnyValue, len(slice))
		for i, item := range slice {
			values[i] = logValueToProto(item)
		}
		return &pb.AnyValue{Value: &pb.AnyValue_ArrayValue{ArrayValue: &pb.ArrayValue{Values: values}}}
	case log.KindMap:
		kvs := v.AsMap()
		pairs := make([]*pb.KeyValue, len(kvs))
		for i, kv := range kvs {
			pairs[i] = logAttributeToProto(kv)
		}
		return &pb.AnyValue{Value: &pb.AnyValue_KvlistValue{KvlistValue: &pb.KeyValueList{Values: pairs}}}
	default:
		// Handles log.KindString and any other unknown types by converting to string
		return &pb.AnyValue{Value: &pb.AnyValue_StringValue{StringValue: v.AsString()}}
	}
}

// metricToProto converts an OpenTelemetry metric to OTLP protobuf format
func metricToProto(m metricdata.Metrics, s instrumentation.Scope, r *resource.Resource) *pb.Metric {
	pbMetric := &pb.Metric{
		Resource:          resourceToProto(r),
		ResourceSchemaUrl: r.SchemaURL(),
		Scope:             instrumentationScopeToProto(s),
		ScopeSchemaUrl:    s.SchemaURL,
		Name:              m.Name,
		Description:       m.Description,
		Unit:              m.Unit,
	}

	switch data := m.Data.(type) {
	case metricdata.Gauge[int64]:
		pbMetric.Data = &pb.Metric_Gauge{
			Gauge: &pb.Gauge{
				DataPoints: numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Gauge[float64]:
		pbMetric.Data = &pb.Metric_Gauge{
			Gauge: &pb.Gauge{
				DataPoints: numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Sum[int64]:
		pbMetric.Data = &pb.Metric_Sum{
			Sum: &pb.Sum{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				IsMonotonic:            data.IsMonotonic,
				DataPoints:             numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Sum[float64]:
		pbMetric.Data = &pb.Metric_Sum{
			Sum: &pb.Sum{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				IsMonotonic:            data.IsMonotonic,
				DataPoints:             numberDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Histogram[int64]:
		pbMetric.Data = &pb.Metric_Histogram{
			Histogram: &pb.Histogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             histogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Histogram[float64]:
		pbMetric.Data = &pb.Metric_Histogram{
			Histogram: &pb.Histogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             histogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.ExponentialHistogram[int64]:
		pbMetric.Data = &pb.Metric_ExponentialHistogram{
			ExponentialHistogram: &pb.ExponentialHistogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             exponentialHistogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.ExponentialHistogram[float64]:
		pbMetric.Data = &pb.Metric_ExponentialHistogram{
			ExponentialHistogram: &pb.ExponentialHistogram{
				AggregationTemporality: aggregationTemporalityToProto(data.Temporality),
				DataPoints:             exponentialHistogramDataPointsToProto(data.DataPoints),
			},
		}
	case metricdata.Summary:
		pbMetric.Data = &pb.Metric_Summary{
			Summary: &pb.Summary{
				DataPoints: summaryDataPointsToProto(data.DataPoints),
			},
		}
	}

	return pbMetric
}

// aggregationTemporalityToProto converts temporality to protobuf format
func aggregationTemporalityToProto(temporality metricdata.Temporality) pb.AggregationTemporality {
	switch temporality {
	case metricdata.CumulativeTemporality:
		return pb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE
	case metricdata.DeltaTemporality:
		return pb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA
	default:
		return pb.AggregationTemporality_AGGREGATION_TEMPORALITY_UNSPECIFIED
	}
}

// numberDataPointsToProto converts number data points to protobuf format
func numberDataPointsToProto[N int64 | float64](points []metricdata.DataPoint[N]) []*pb.NumberDataPoint {
	pbPoints := make([]*pb.NumberDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &pb.NumberDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Exemplars:         exemplarsToProto(dp.Exemplars),
		}

		// Convert attributes
		attrs := dp.Attributes.ToSlice()
		pbPoint.Attributes = make([]*pb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		// Set value based on type
		switch v := any(dp.Value).(type) {
		case int64:
			pbPoint.Value = &pb.NumberDataPoint_AsInt{AsInt: v}
		case float64:
			pbPoint.Value = &pb.NumberDataPoint_AsDouble{AsDouble: v}
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// histogramDataPointsToProto converts histogram data points to protobuf format
func histogramDataPointsToProto[N int64 | float64](points []metricdata.HistogramDataPoint[N]) []*pb.HistogramDataPoint {
	pbPoints := make([]*pb.HistogramDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &pb.HistogramDataPoint{
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
		pbPoint.Attributes = make([]*pb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// exponentialHistogramDataPointsToProto converts exponential histogram data points to protobuf format
func exponentialHistogramDataPointsToProto[N int64 | float64](points []metricdata.ExponentialHistogramDataPoint[N]) []*pb.ExponentialHistogramDataPoint {
	pbPoints := make([]*pb.ExponentialHistogramDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &pb.ExponentialHistogramDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Count:             dp.Count,
			Scale:             dp.Scale,
			ZeroCount:         dp.ZeroCount,
			Exemplars:         exemplarsToProto(dp.Exemplars),
			Positive: &pb.ExponentialHistogramDataPoint_Buckets{
				Offset:       dp.PositiveBucket.Offset,
				BucketCounts: dp.PositiveBucket.Counts,
			},
			Negative: &pb.ExponentialHistogramDataPoint_Buckets{
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
		pbPoint.Attributes = make([]*pb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// summaryDataPointsToProto converts summary data points to protobuf format
func summaryDataPointsToProto(points []metricdata.SummaryDataPoint) []*pb.SummaryDataPoint {
	pbPoints := make([]*pb.SummaryDataPoint, len(points))
	for i, dp := range points {
		pbPoint := &pb.SummaryDataPoint{
			StartTimeUnixNano: int64ToUint64(dp.StartTime.UnixNano()),
			TimeUnixNano:      int64ToUint64(dp.Time.UnixNano()),
			Count:             dp.Count,
			Sum:               dp.Sum,
		}

		// Convert quantile values
		pbPoint.QuantileValues = make([]*pb.SummaryDataPoint_ValueAtQuantile, len(dp.QuantileValues))
		for j, qv := range dp.QuantileValues {
			pbPoint.QuantileValues[j] = &pb.SummaryDataPoint_ValueAtQuantile{
				Quantile: qv.Quantile,
				Value:    qv.Value,
			}
		}

		// Convert attributes
		attrs := dp.Attributes.ToSlice()
		pbPoint.Attributes = make([]*pb.KeyValue, len(attrs))
		for j, attr := range attrs {
			pbPoint.Attributes[j] = attributeToProto(attr)
		}

		pbPoints[i] = pbPoint
	}
	return pbPoints
}

// exemplarsToProto converts exemplars to protobuf format
func exemplarsToProto[N int64 | float64](exemplars []metricdata.Exemplar[N]) []*pb.Exemplar {
	if len(exemplars) == 0 {
		return nil
	}

	pbExemplars := make([]*pb.Exemplar, len(exemplars))
	for i, ex := range exemplars {
		pbEx := &pb.Exemplar{
			TimeUnixNano: int64ToUint64(ex.Time.UnixNano()),
		}

		// Set value based on type
		switch v := any(ex.Value).(type) {
		case int64:
			pbEx.Value = &pb.Exemplar_AsInt{AsInt: v}
		case float64:
			pbEx.Value = &pb.Exemplar_AsDouble{AsDouble: v}
		}

		// Add trace context if present
		if len(ex.TraceID) > 0 {
			pbEx.TraceId = ex.TraceID
		}
		if len(ex.SpanID) > 0 {
			pbEx.SpanId = ex.SpanID
		}

		// Convert filtered attributes
		pbEx.FilteredAttributes = make([]*pb.KeyValue, len(ex.FilteredAttributes))
		for j, attr := range ex.FilteredAttributes {
			pbEx.FilteredAttributes[j] = attributeToProto(attr)
		}

		pbExemplars[i] = pbEx
	}
	return pbExemplars
}

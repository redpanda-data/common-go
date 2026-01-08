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

	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// LogExporter implements the OpenTelemetry log exporter interface.
type LogExporter struct {
	*Exporter
}

// Ensure LogExporter implements the Exporter interface
var _ sdklog.Exporter = (*LogExporter)(nil)

// NewLogExporter creates a new log exporter that writes to Kafka.
// By default, logs are written to the "otel-logs" topic, which can be overridden using WithTopic.
//
// Required: WithBrokers option must be provided.
//
// Example:
//
//	exporter, err := NewLogExporter(
//	    WithBrokers("localhost:9092"),
//	    WithResource(res),
//	)
//
// To use a custom topic:
//
//	exporter, err := NewLogExporter(
//	    WithBrokers("localhost:9092"),
//	    WithTopic("custom-logs"),
//	)
func NewLogExporter(opts ...Option) (*LogExporter, error) {
	exporter, err := newExporter("otel-logs", opts...)
	if err != nil {
		return nil, err
	}

	return &LogExporter{
		Exporter: exporter,
	}, nil
}

// Export exports a batch of log records to Kafka.
func (e *LogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	signals := make([]signalRecord, len(records))
	for i, rec := range records {
		signals[i] = signalRecord{
			key:     nil,
			payload: logRecordToProto(rec),
		}
	}
	return e.export(
		ctx,
		signals,
		otlpLogProtoSchema,
		otlpLogJSONSchema,
	)
}

// ForceFlush flushes any pending log records.
func (e *LogExporter) ForceFlush(ctx context.Context) error {
	return e.client.Flush(ctx)
}

// Shutdown shuts down the exporter.
func (e *LogExporter) Shutdown(ctx context.Context) error {
	return e.Exporter.Shutdown(ctx)
}

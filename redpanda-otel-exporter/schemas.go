// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package redpandaotelexporter

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/twmb/franz-go/pkg/sr"
)

// OTLP Protobuf schemas - these are the official OTLP proto definitions

//go:embed proto/common.proto
var otlpCommonProtoSchema string

//go:embed proto/trace.proto
var otlpTraceProtoSchema string

//go:embed proto/metric.proto
var otlpMetricProtoSchema string

//go:embed proto/log.proto
var otlpLogProtoSchema string

// OTLP JSON schemas - JSON Schema definitions for OTLP JSON format

//go:embed proto/trace.schema.json
var otlpTraceJSONSchema string

//go:embed proto/metric.schema.json
var otlpMetricJSONSchema string

//go:embed proto/log.schema.json
var otlpLogJSONSchema string

// Default subject names for Schema Registry
const (
	// DefaultCommonSubject is the default subject for common.proto schema
	DefaultCommonSubject = "redpanda-otel-common"
	// DefaultCommonSubjectJSON is the default subject for common schema in JSON format
	DefaultCommonSubjectJSON = "redpanda-otel-common-json"

	// DefaultTraceSubject is the default subject for trace schema
	DefaultTraceSubject = "redpanda-otel-traces"
	// DefaultTraceSubjectJSON is the default subject for trace schema in JSON format
	DefaultTraceSubjectJSON = "redpanda-otel-traces-json"

	// DefaultMetricSubject is the default subject for metric schema
	DefaultMetricSubject = "redpanda-otel-metrics"
	// DefaultMetricSubjectJSON is the default subject for metric schema in JSON format
	DefaultMetricSubjectJSON = "redpanda-otel-metrics-json"

	// DefaultLogSubject is the default subject for log schema
	DefaultLogSubject = "redpanda-otel-logs"
	// DefaultLogSubjectJSON is the default subject for log schema in JSON format
	DefaultLogSubjectJSON = "redpanda-otel-logs-json"
)

// RegisterCommonProtoSchema registers the common.proto schema with the Schema Registry
// and returns a schema reference that can be used by other schemas.
func RegisterCommonProtoSchema(ctx context.Context, client *sr.Client, subject string) (sr.SchemaReference, error) {
	schema := sr.Schema{
		Schema: otlpCommonProtoSchema,
		Type:   sr.TypeProtobuf,
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return sr.SchemaReference{}, fmt.Errorf("register common schema for subject %s: %w", subject, err)
	}

	return sr.SchemaReference{
		Name:    "redpanda/otel/v1/common.proto",
		Subject: subject,
		Version: ss.Version,
	}, nil
}

// RegisterTraceProtoSchema registers the trace.proto schema with the Schema Registry.
func RegisterTraceProtoSchema(ctx context.Context, client *sr.Client, subject string, commonRef sr.SchemaReference) (*sr.SubjectSchema, error) {
	schema := sr.Schema{
		Schema:     otlpTraceProtoSchema,
		Type:       sr.TypeProtobuf,
		References: []sr.SchemaReference{commonRef},
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return nil, fmt.Errorf("register trace schema for subject %s: %w", subject, err)
	}
	return &ss, nil
}

// RegisterMetricProtoSchema registers the metric.proto schema with the Schema Registry.
func RegisterMetricProtoSchema(ctx context.Context, client *sr.Client, subject string, commonRef sr.SchemaReference) (*sr.SubjectSchema, error) {
	schema := sr.Schema{
		Schema:     otlpMetricProtoSchema,
		Type:       sr.TypeProtobuf,
		References: []sr.SchemaReference{commonRef},
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return nil, fmt.Errorf("register metric schema for subject %s: %w", subject, err)
	}
	return &ss, nil
}

// RegisterLogProtoSchema registers the log.proto schema with the Schema Registry.
func RegisterLogProtoSchema(ctx context.Context, client *sr.Client, subject string, commonRef sr.SchemaReference) (*sr.SubjectSchema, error) {
	schema := sr.Schema{
		Schema:     otlpLogProtoSchema,
		Type:       sr.TypeProtobuf,
		References: []sr.SchemaReference{commonRef},
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return nil, fmt.Errorf("register log schema for subject %s: %w", subject, err)
	}
	return &ss, nil
}

// RegisterTraceJSONSchema registers the trace JSON schema with the Schema Registry.
func RegisterTraceJSONSchema(ctx context.Context, client *sr.Client, subject string) (*sr.SubjectSchema, error) {
	schema := sr.Schema{
		Schema: otlpTraceJSONSchema,
		Type:   sr.TypeJSON,
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return nil, fmt.Errorf("register trace JSON schema for subject %s: %w", subject, err)
	}
	return &ss, nil
}

// RegisterMetricJSONSchema registers the metric JSON schema with the Schema Registry.
func RegisterMetricJSONSchema(ctx context.Context, client *sr.Client, subject string) (*sr.SubjectSchema, error) {
	schema := sr.Schema{
		Schema: otlpMetricJSONSchema,
		Type:   sr.TypeJSON,
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return nil, fmt.Errorf("register metric JSON schema for subject %s: %w", subject, err)
	}
	return &ss, nil
}

// RegisterLogJSONSchema registers the log JSON schema with the Schema Registry.
func RegisterLogJSONSchema(ctx context.Context, client *sr.Client, subject string) (*sr.SubjectSchema, error) {
	schema := sr.Schema{
		Schema: otlpLogJSONSchema,
		Type:   sr.TypeJSON,
	}
	ss, err := client.CreateSchema(ctx, subject, schema)
	if err != nil {
		return nil, fmt.Errorf("register log JSON schema for subject %s: %w", subject, err)
	}
	return &ss, nil
}

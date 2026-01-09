// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package redpandaotelexporter

import _ "embed"

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

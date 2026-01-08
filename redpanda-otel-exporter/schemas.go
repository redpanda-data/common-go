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

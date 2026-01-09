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

package log

import (
	"context"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// BatchProcessoris a wrapper around an sdklog.BatchProcessor with level filtering support
type BatchProcessor struct {
	*sdklog.BatchProcessor
	severity otellog.Severity
}

func (p *BatchProcessor) Enabled(ctx context.Context, param sdklog.EnabledParameters) bool {
	return shouldLogOTEL(p.severity, param.Severity)
}

func NewBatchProcessor(exporter sdklog.Exporter, severity otellog.Severity, opts ...sdklog.BatchProcessorOption) *BatchProcessor {
	return &BatchProcessor{
		BatchProcessor: sdklog.NewBatchProcessor(exporter, opts...),
		severity:       severity,
	}
}

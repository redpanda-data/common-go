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

	"github.com/go-logr/logr"
)

type contextFreeSink struct {
	logr.LogSink
}

// ContextFree returns a logr.LogSink that filters out any context.Context
// key/value pairs so the sink can be used without carrying context values.
func ContextFree(sink logr.LogSink) logr.LogSink {
	return &contextFreeSink{
		LogSink: sink,
	}
}

func (l contextFreeSink) WithValues(kvList ...any) logr.LogSink {
	return ContextFree(l.LogSink.WithValues(filterContexts(kvList)...))
}

func (l contextFreeSink) Info(level int, msg string, kvList ...any) {
	l.LogSink.Info(level, msg, filterContexts(kvList)...)
}

func (l contextFreeSink) Error(err error, msg string, kvList ...any) {
	l.LogSink.Error(err, msg, filterContexts(kvList)...)
}

func filterContexts(kvList []any) []any {
	filtered := []any{}
	if len(kvList)%2 != 0 {
		kvList = append(kvList, nil)
	}
	for i := 0; i < len(kvList); i += 2 {
		if _, ok := kvList[i+1].(context.Context); !ok {
			filtered = append(filtered, kvList[i], kvList[i+1])
		}
	}

	return filtered
}

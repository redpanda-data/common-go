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
	"log/slog"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Info logs a non-error message using the logger stored in ctx. Additional
// key/value pairs are forwarded to the underlying logger.
func Info(ctx context.Context, msg string, keysAndValues ...any) {
	FromContext(ctx).Info(msg, keysAndValues...)
}

// Error logs an error with message and additional key/value pairs using the
// logger stored in ctx.
func Error(ctx context.Context, err error, msg string, keysAndValues ...any) {
	FromContext(ctx).Error(err, msg, keysAndValues...)
}

// IntoContext takes a context and sets the logger as one of its values.
// Use FromContext function to retrieve the logger.
var IntoContext = log.IntoContext

// FromContext returns a logger with predefined values from a context.Context.
func FromContext(ctx context.Context, keysAndValues ...any) logr.Logger {
	keysAndValues = append(keysAndValues, "ctx", ctx)
	return log.FromContext(ctx, keysAndValues...)
}

// SetGlobals sets the global [logr.Logger] instance for this package and all
// logging libraries that may be used by 3rd party dependencies.
func SetGlobals(l logr.Logger) {
	// if this isn't an OTEL log sink, filter out the "ctx" parameter when logging
	// out values
	if _, ok := l.GetSink().(*LogSink); !ok {
		l = l.WithSink(ContextFree(l.GetSink()))
	}

	log.SetLogger(l)
	klog.SetLogger(l)
	slog.SetDefault(
		slog.New(logr.ToSlogHandler(l)),
	)
}

// MultiSink is a [logr.LogSink] that delegates to one or more other
// [logr.LogSink]s.
type MultiSink struct {
	Sinks []logr.LogSink
}

var _ logr.LogSink = &MultiSink{}

// Init calls Init on all underlying sinks.
func (s *MultiSink) Init(info logr.RuntimeInfo) {
	for _, sink := range s.Sinks {
		sink.Init(info)
	}
}

// Enabled returns true if any delegated sink is enabled at the provided
// verbosity level.
func (s *MultiSink) Enabled(level int) bool {
	for _, sink := range s.Sinks {
		if sink.Enabled(level) {
			return true
		}
	}
	return false
}

// Info forwards an informational log message to all enabled delegated sinks.
func (s *MultiSink) Info(level int, msg string, keysAndValues ...any) {
	for _, sink := range s.Sinks {
		if sink.Enabled(level) {
			sink.Info(level, msg, keysAndValues...)
		}
	}
}

// Error forwards an error log message to all delegated sinks.
func (s *MultiSink) Error(err error, msg string, keysAndValues ...any) {
	for _, sink := range s.Sinks {
		sink.Error(err, msg, keysAndValues...)
	}
}

// WithValues returns a new MultiSink where each delegated sink has been
// decorated with the provided key/value pairs.
func (s *MultiSink) WithValues(keysAndValues ...any) logr.LogSink {
	sinks := make([]logr.LogSink, len(s.Sinks))
	for i, sink := range s.Sinks {
		sinks[i] = sink.WithValues(keysAndValues...)
	}
	return &MultiSink{Sinks: sinks}
}

// WithName returns a new MultiSink where each delegated sink has its name
// appended with the provided value.
func (s *MultiSink) WithName(name string) logr.LogSink {
	sinks := make([]logr.LogSink, len(s.Sinks))
	for i, sink := range s.Sinks {
		sinks[i] = sink.WithName(name)
	}
	return &MultiSink{Sinks: sinks}
}

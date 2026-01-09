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

package kube

import (
	"context"
	"sync"

	"github.com/go-logr/logr"
)

// DebounceErrorFunc is a function that can be used to debounce error logging for messages that occur
// frequently, but we don't want to spam logs with.
type DebounceErrorFunc func(logger logr.Logger, err error, msg string, keysAndValues ...any)

var (
	globalDebounceErrorFunc DebounceErrorFunc = func(logger logr.Logger, err error, msg string, keysAndValues ...any) {
		// by default we don't do any debouncing
		logger.Error(err, msg, keysAndValues...)
	}
	debounceMutex sync.RWMutex
)

// SetDebounceErrorFunc sets the global debounce error function.
func SetDebounceErrorFunc(f DebounceErrorFunc) {
	debounceMutex.Lock()
	globalDebounceErrorFunc = f
	debounceMutex.Unlock()
}

// DebounceError logs an error message using the global debounce error function.
func DebounceError(logger logr.Logger, err error, msg string, keysAndValues ...any) {
	debounceMutex.RLock()
	defer debounceMutex.RUnlock()
	globalDebounceErrorFunc(logger, err, msg, keysAndValues...)
}

// ContextLoggerFactory is a logger factory function that takes a context, likely to pull an underlying logger
// instance from it.
type ContextLoggerFactory func(ctx context.Context) logr.Logger

var (
	globalContextLoggerFactory ContextLoggerFactory = func(_ context.Context) logr.Logger { return logr.Discard() }
	loggerMutex                sync.RWMutex
)

// SetContextLoggerFactory sets the global context logger.
func SetContextLoggerFactory(factory ContextLoggerFactory) {
	loggerMutex.Lock()
	globalContextLoggerFactory = factory
	loggerMutex.Unlock()
}

// Logger initializes a new logr.Logger instance from the given context.
func Logger(ctx context.Context) logr.Logger {
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()
	return globalContextLoggerFactory(ctx)
}

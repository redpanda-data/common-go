// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

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

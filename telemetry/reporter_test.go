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

package telemetry

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeLogger struct {
	mu   sync.Mutex
	msgs []string
}

func (f *fakeLogger) Debug(msg string, kv ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, msg)
}

func (f *fakeLogger) has(msg string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.msgs {
		if m == msg {
			return true
		}
	}
	return false
}

func TestReporterRunSendsPeriodically(t *testing.T) {
	var sends int32
	r := &Reporter{
		Client: &Client{disabled: true},
		Collector: func(ctx context.Context) (any, error) {
			atomic.AddInt32(&sends, 1)
			return map[string]string{"a": "b"}, nil
		},
		Delay:  5 * time.Millisecond,
		Period: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	require.Eventually(t, func() bool { return atomic.LoadInt32(&sends) >= 3 },
		2*time.Second, 1*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestReporterRunSurvivesCollectorError(t *testing.T) {
	var calls int32
	r := &Reporter{
		Client: &Client{disabled: true},
		Collector: func(ctx context.Context) (any, error) {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				return nil, context.DeadlineExceeded
			}
			return map[string]string{"ok": "1"}, nil
		},
		Delay:  5 * time.Millisecond,
		Period: 10 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) >= 3 },
		2*time.Second, 1*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

func TestReporterLogsCollectorError(t *testing.T) {
	fl := &fakeLogger{}
	r := &Reporter{
		Client: &Client{disabled: true},
		Collector: func(ctx context.Context) (any, error) {
			return nil, errors.New("boom")
		},
		Delay:  5 * time.Millisecond,
		Period: 10 * time.Millisecond,
		Logger: fl,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	require.Eventually(t, func() bool { return fl.has("failed to collect telemetry") },
		2*time.Second, 1*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

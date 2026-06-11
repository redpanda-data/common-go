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

func (f *fakeLogger) Debug(msg string, _ ...any) {
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

// enabledClient returns an enabled (non-disabled) Client so Reporter.Run runs
// its loop. It points at an unreachable endpoint with retries disabled, so Send
// fails fast and is dropped — these tests assert on Collector invocations, not
// on send success.
func enabledClient(t *testing.T) *Client {
	t.Helper()
	priv, _ := genKeyPEM(t)
	c, err := New(Config{
		Endpoint:      "http://127.0.0.1:1",
		Path:          "/t",
		SigningKeyPEM: priv,
		RetryCount:    -1,
		Timeout:       100 * time.Millisecond,
	})
	require.NoError(t, err)
	require.False(t, c.Disabled())
	return c
}

func TestReporterRunSendsPeriodically(t *testing.T) {
	var sends int32
	r := &Reporter{
		Client: enabledClient(t),
		Collector: func(_ context.Context) (any, error) {
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
		Client: enabledClient(t),
		Collector: func(_ context.Context) (any, error) {
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
		Client: enabledClient(t),
		Collector: func(_ context.Context) (any, error) {
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

// TestReporterRunDisabledClientSkipsCollection verifies a disabled client makes
// Run wait for shutdown without ever invoking the (potentially expensive)
// Collector.
func TestReporterRunDisabledClientSkipsCollection(t *testing.T) {
	c, err := New(Config{Path: "/t"}) // no signing key -> disabled
	require.NoError(t, err)
	require.True(t, c.Disabled())

	var calls int32
	r := &Reporter{
		Client:    c,
		Collector: func(_ context.Context) (any, error) { atomic.AddInt32(&calls, 1); return nil, nil },
		Delay:     time.Millisecond,
		Period:    time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(50 * time.Millisecond) // ample time for many periods to elapse
	require.Zero(t, atomic.LoadInt32(&calls), "disabled client must not invoke Collector")
	cancel()
	require.NoError(t, <-done)
}

// TestReporterRunRequiresClientAndCollector locks in the required-field error
// path: in a controller-runtime manager a non-nil Runnable.Start error shuts
// down the whole process, so this must fail loudly rather than silently no-op.
func TestReporterRunRequiresClientAndCollector(t *testing.T) {
	err := (&Reporter{
		Collector: func(_ context.Context) (any, error) { return nil, nil },
	}).Run(context.Background())
	require.Error(t, err, "nil Client must error")

	c, err := New(Config{Path: "/t"})
	require.NoError(t, err)
	err = (&Reporter{Client: c}).Run(context.Background())
	require.Error(t, err, "nil Collector must error")
}

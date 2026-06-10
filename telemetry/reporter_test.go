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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReporterRunSendsPeriodically(t *testing.T) {
	var sends int32
	r := &Reporter{
		Client: &Client{disabled: true},
		Collector: func(ctx context.Context) (any, error) {
			atomic.AddInt32(&sends, 1)
			return map[string]string{"a": "b"}, nil
		},
		Delay:  1 * time.Millisecond,
		Period: 5 * time.Millisecond,
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
		Delay:  1 * time.Millisecond,
		Period: 2 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	require.Eventually(t, func() bool { return atomic.LoadInt32(&calls) >= 3 },
		2*time.Second, 1*time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}

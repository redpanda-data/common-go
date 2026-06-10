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
	"time"
)

const (
	defaultDelay  = 5 * time.Minute
	defaultPeriod = 24 * time.Hour
)

// Reporter runs a periodic collect→send loop with drop-on-error semantics.
type Reporter struct {
	Client    *Client
	Collector func(ctx context.Context) (any, error)
	Delay     time.Duration
	Period    time.Duration
	Logger    Logger
}

func (r *Reporter) debug(msg string, kv ...any) {
	if r.Logger != nil {
		r.Logger.Debug(msg, kv...)
	}
}

// Run blocks until ctx is cancelled, returning nil on cancellation. Collection
// and send errors are debug-logged and dropped; the loop continues.
func (r *Reporter) Run(ctx context.Context) error {
	delay := r.Delay
	if delay == 0 {
		delay = defaultDelay
	}
	period := r.Period
	if period == 0 {
		period = defaultPeriod
	}

	select {
	case <-ctx.Done():
		return nil
	case <-time.After(delay):
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		r.collectAndSend(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Reporter) collectAndSend(ctx context.Context) {
	payload, err := r.Collector(ctx)
	if err != nil {
		r.debug("failed to collect telemetry", "error", err)
		return
	}
	if err := r.Client.Send(ctx, payload); err != nil {
		r.debug("failed to send telemetry", "error", err)
	}
}

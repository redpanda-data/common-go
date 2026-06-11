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
	"time"
)

const (
	defaultDelay  = 5 * time.Minute
	defaultPeriod = 24 * time.Hour
)

// Reporter runs a periodic collect→send loop with drop-on-error semantics.
//
// Client and Collector are required and must be non-nil; Run returns an error
// if either is missing.
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

// maxCollectTimeout caps the per-cycle collection timeout.
const maxCollectTimeout = 5 * time.Minute

// Run blocks until ctx is cancelled, returning nil on cancellation. Collection
// and send errors are debug-logged and dropped; the loop continues.
func (r *Reporter) Run(ctx context.Context) error {
	if r.Client == nil {
		return errors.New("telemetry: Reporter.Client is required")
	}
	if r.Collector == nil {
		return errors.New("telemetry: Reporter.Collector is required")
	}

	// A disabled client (no signing key) can never become enabled — Config is
	// immutable after New — so skip the loop entirely rather than running the
	// (potentially expensive, cluster-wide) Collector every period only to
	// no-op the Send. Just wait for shutdown.
	if r.Client.Disabled() {
		<-ctx.Done()
		return nil
	}

	delay := r.Delay
	if delay == 0 {
		delay = defaultDelay
	}
	period := r.Period
	if period == 0 {
		period = defaultPeriod
	}

	// No jitter is applied to Delay/Period: process start times already
	// decorrelate installs. Do not switch this to a fixed wall-clock schedule
	// without adding jitter, or every install would report in lockstep.
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil
	case <-timer.C:
	}

	// Bound each collection so a consumer Collector that ignores ctx and hangs
	// cannot wedge the loop forever. Half the period, capped at maxCollectTimeout.
	collectTimeout := period / 2
	if collectTimeout > maxCollectTimeout {
		collectTimeout = maxCollectTimeout
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()
	for {
		r.collectAndSend(ctx, collectTimeout)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Reporter) collectAndSend(ctx context.Context, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := r.Collector(ctx)
	if err != nil {
		r.debug("failed to collect telemetry", "error", err)
		return
	}
	if err := r.Client.Send(ctx, payload); err != nil {
		r.debug("failed to send telemetry", "error", err)
	}
}

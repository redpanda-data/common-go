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

package kvstore

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestClient creates a Client with only sync-related fields initialized.
// No Kafka connection is made.
func newTestClient() *Client {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		consumerCtx:    ctx,
		consumerCancel: cancel,
		consumerOffset: -1,
		syncBroadcast:  make(chan struct{}),
	}
	return c
}

// setOffset sets the consumer offset under the lock, simulating what runConsumer does.
func (c *Client) setOffset(offset int64) {
	c.syncMu.Lock()
	c.consumerOffset = offset
	close(c.syncBroadcast)
	c.syncBroadcast = make(chan struct{})
	c.syncMu.Unlock()
}

func TestSync_AlreadyAtOffset(t *testing.T) {
	c := newTestClient()
	defer c.consumerCancel()

	c.consumerOffset = 10

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := c.sync(ctx, 5)
	require.NoError(t, err)
}

func TestSync_WaitsForOffset(t *testing.T) {
	c := newTestClient()
	defer c.consumerCancel()

	c.consumerOffset = 0

	done := make(chan error)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		done <- c.sync(ctx, 10)
	}()

	// Simulate consumer advancing
	time.Sleep(10 * time.Millisecond)
	c.setOffset(10)

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("sync did not return")
	}
}

func TestSync_ContextCancellation(t *testing.T) {
	c := newTestClient()
	defer c.consumerCancel()

	c.consumerOffset = 0

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	go func() {
		done <- c.sync(ctx, 100)
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("sync did not return after context cancel")
	}
}

func TestSync_ClientClosed(t *testing.T) {
	c := newTestClient()
	defer c.consumerCancel()

	c.consumerOffset = 0

	done := make(chan error)
	go func() {
		ctx := context.Background()
		done <- c.sync(ctx, 100)
	}()

	time.Sleep(10 * time.Millisecond)
	c.closed.Store(true)
	// Wake up waiters by closing broadcast channel
	c.syncMu.Lock()
	close(c.syncBroadcast)
	c.syncMu.Unlock()

	select {
	case err := <-done:
		require.ErrorIs(t, err, ErrClientClosed)
	case <-time.After(time.Second):
		t.Fatal("sync did not return after close")
	}
}

func TestSync_ConcurrentWaiters(t *testing.T) {
	c := newTestClient()
	defer c.consumerCancel()

	c.consumerOffset = 0

	const numWaiters = 100
	done := make(chan error, numWaiters)

	for i := 0; i < numWaiters; i++ {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done <- c.sync(ctx, 50)
		}()
	}

	// Let all goroutines start waiting
	time.Sleep(50 * time.Millisecond)

	// Advance offset
	c.setOffset(50)

	// All should complete
	for i := 0; i < numWaiters; i++ {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatalf("waiter %d did not complete", i)
		}
	}
}

func TestSync_NoSpuriousWakeups(t *testing.T) {
	c := newTestClient()
	defer c.consumerCancel()

	c.consumerOffset = 0

	done := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.sync(ctx, 100)
		close(done)
	}()

	// Send broadcasts at intermediate offsets
	var wg sync.WaitGroup
	for i := 1; i < 100; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			c.setOffset(int64(offset))
		}(i)
	}
	wg.Wait()

	// Should still be blocking
	select {
	case <-done:
		t.Fatal("sync returned before reaching target offset")
	case <-time.After(10 * time.Millisecond):
	}

	// Now reach target
	c.setOffset(100)

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Fatal("sync did not return after reaching target")
	}
}

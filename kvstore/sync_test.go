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

package kvstore_test

import (
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"

	kv "github.com/redpanda-data/common-go/kvstore"
	"github.com/redpanda-data/common-go/kvstore/memdb"
)

func TestSync_PutBlocksUntilVisible(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-sync-single", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	// Put blocks until visible
	err = client.Put(ctx, []byte("key1"), []byte("value1"))
	require.NoError(t, err)

	// Immediately readable - no sleep needed
	val, err := client.Get(ctx, []byte("key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), val)
}

func TestSync_ConcurrentPuts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-sync-concurrent", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	const numWrites = 1000

	// Concurrent writes
	var wg sync.WaitGroup
	errCount := make(chan struct{}, numWrites)

	for i := 0; i < numWrites; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := []byte(fmt.Sprintf("key-%d", i))
			value := fmt.Sprintf("value-%d", i)
			if err := client.Put(ctx, key, []byte(value)); err != nil {
				errCount <- struct{}{}
			}
		}(i)
	}

	wg.Wait()
	close(errCount)

	require.Empty(t, errCount, "expected no errors")

	// Spot check - verify a sample of writes
	for i := 0; i < numWrites; i += 100 {
		key := []byte(fmt.Sprintf("key-%d", i))
		expectedValue := fmt.Sprintf("value-%d", i)

		val, err := client.Get(ctx, key)
		require.NoError(t, err, "key %s should exist", key)
		assert.Equal(t, []byte(expectedValue), val, "key %s should have correct value", key)
	}
}

func TestSync_BatchWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-sync-batch", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	// Build a batch with puts and deletes
	batch := client.Batch()
	for i := 0; i < 100; i++ {
		batch.Put([]byte(fmt.Sprintf("batch-key-%d", i)), []byte(fmt.Sprintf("batch-value-%d", i)))
	}

	err = batch.Execute(ctx)
	require.NoError(t, err)

	// All should be immediately visible
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("batch-key-%d", i))
		val, err := client.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("batch-value-%d", i)), val)
	}

	// Now batch delete some and add new ones
	batch2 := client.Batch()
	for i := 0; i < 50; i++ {
		batch2.Delete([]byte(fmt.Sprintf("batch-key-%d", i)))
	}
	for i := 100; i < 150; i++ {
		batch2.Put([]byte(fmt.Sprintf("batch-key-%d", i)), []byte(fmt.Sprintf("batch-value-%d", i)))
	}

	err = batch2.Execute(ctx)
	require.NoError(t, err)

	// Deleted keys should return ErrNotFound
	for i := 0; i < 50; i++ {
		key := []byte(fmt.Sprintf("batch-key-%d", i))
		_, err := client.Get(ctx, key)
		require.ErrorIs(t, err, kv.ErrNotFound, "key %s should be deleted", key)
	}

	// Original keys 50-99 should still exist
	for i := 50; i < 100; i++ {
		key := []byte(fmt.Sprintf("batch-key-%d", i))
		val, err := client.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("batch-value-%d", i)), val)
	}

	// New keys 100-149 should exist
	for i := 100; i < 150; i++ {
		key := []byte(fmt.Sprintf("batch-key-%d", i))
		val, err := client.Get(ctx, key)
		require.NoError(t, err)
		assert.Equal(t, []byte(fmt.Sprintf("batch-value-%d", i)), val)
	}
}

func TestSync_BootstrapFromExistingData(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	const topic = "test-bootstrap"

	// First client: write some data
	storage1, err := memdb.New()
	require.NoError(t, err)

	client1, err := kv.NewClient(ctx, topic, storage1,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		err = client1.Put(ctx, []byte(fmt.Sprintf("key-%d", i)), []byte(fmt.Sprintf("value-%d", i)))
		require.NoError(t, err)
	}
	client1.Close()

	// Second client: fresh storage, should bootstrap from Kafka
	storage2, err := memdb.New()
	require.NoError(t, err)

	client2, err := kv.NewClient(ctx, topic, storage2,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client2.Close()

	// Data should be immediately available - no race, no waiting
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		val, err := client2.Get(ctx, key)
		require.NoError(t, err, "key %s should exist immediately after bootstrap", key)
		assert.Equal(t, []byte(fmt.Sprintf("value-%d", i)), val)
	}
}

func TestSync_LargeBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-sync-large-batch", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	const batchSize = 10_000

	batch := client.Batch()
	for i := 0; i < batchSize; i++ {
		batch.Put([]byte(fmt.Sprintf("large-batch-key-%d", i)), []byte(fmt.Sprintf("large-batch-value-%d", i)))
	}

	err = batch.Execute(ctx)
	require.NoError(t, err)

	// All should be immediately visible
	for i := 0; i < batchSize; i += 1000 {
		key := []byte(fmt.Sprintf("large-batch-key-%d", i))
		val, err := client.Get(ctx, key)
		require.NoError(t, err, "key %s should exist", key)
		assert.Equal(t, []byte(fmt.Sprintf("large-batch-value-%d", i)), val)
	}
}

// TestMultiClient_EventualConsistency verifies that multiple clients operating
// concurrently eventually converge to the same state.
func TestMultiClient_EventualConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	const topic = "test-multi-client"

	// Create two clients on the same topic
	storage1, err := memdb.New()
	require.NoError(t, err)

	client1, err := kv.NewClient(ctx, topic, storage1,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client1.Close()

	storage2, err := memdb.New()
	require.NoError(t, err)

	client2, err := kv.NewClient(ctx, topic, storage2,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client2.Close()

	// Client 1 writes some data
	err = client1.Put(ctx, []byte("client1-key1"), []byte("client1-value1"))
	require.NoError(t, err)

	err = client1.Put(ctx, []byte("client1-key2"), []byte("client1-value2"))
	require.NoError(t, err)

	// Client 1 can immediately read its own writes
	val, err := client1.Get(ctx, []byte("client1-key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("client1-value1"), val)

	// Client 2 should eventually see Client 1's writes (with retries for eventual consistency)
	require.Eventually(t, func() bool {
		val, err := client2.Get(ctx, []byte("client1-key1"))
		return err == nil && string(val) == "client1-value1"
	}, 5*time.Second, 100*time.Millisecond, "client2 should eventually see client1's write")

	require.Eventually(t, func() bool {
		val, err := client2.Get(ctx, []byte("client1-key2"))
		return err == nil && string(val) == "client1-value2"
	}, 5*time.Second, 100*time.Millisecond, "client2 should eventually see client1's second write")

	// Client 2 writes different keys
	err = client2.Put(ctx, []byte("client2-key1"), []byte("client2-value1"))
	require.NoError(t, err)

	err = client2.Put(ctx, []byte("client2-key2"), []byte("client2-value2"))
	require.NoError(t, err)

	// Client 2 can immediately read its own writes
	val, err = client2.Get(ctx, []byte("client2-key1"))
	require.NoError(t, err)
	assert.Equal(t, []byte("client2-value1"), val)

	// Client 1 should eventually see Client 2's writes
	require.Eventually(t, func() bool {
		val, err := client1.Get(ctx, []byte("client2-key1"))
		return err == nil && string(val) == "client2-value1"
	}, 5*time.Second, 100*time.Millisecond, "client1 should eventually see client2's write")

	require.Eventually(t, func() bool {
		val, err := client1.Get(ctx, []byte("client2-key2"))
		return err == nil && string(val) == "client2-value2"
	}, 5*time.Second, 100*time.Millisecond, "client1 should eventually see client2's second write")

	// Both clients should now see all 4 keys
	allKeys := []string{"client1-key1", "client1-key2", "client2-key1", "client2-key2"}
	for _, key := range allKeys {
		_, err := client1.Get(ctx, []byte(key))
		require.NoError(t, err, "client1 should see key %s", key)

		_, err = client2.Get(ctx, []byte(key))
		require.NoError(t, err, "client2 should see key %s", key)
	}
}

// TestMultiClient_ConcurrentWrites verifies that multiple clients writing
// concurrently all eventually converge to the same state.
func TestMultiClient_ConcurrentWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	const topic = "test-multi-client-concurrent"
	const numClients = 3
	const writesPerClient = 100

	// Create multiple clients
	clients := make([]*kv.Client, numClients)
	for i := 0; i < numClients; i++ {
		storage, err := memdb.New()
		require.NoError(t, err)

		client, err := kv.NewClient(ctx, topic, storage,
			kv.WithBrokers(brokers),
		)
		require.NoError(t, err)
		clients[i] = client
	}
	defer func() {
		for _, client := range clients {
			client.Close()
		}
	}()

	// Each client writes its own set of keys concurrently
	var wg sync.WaitGroup
	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			client := clients[clientID]
			for i := 0; i < writesPerClient; i++ {
				key := []byte(fmt.Sprintf("client%d-key%d", clientID, i))
				value := []byte(fmt.Sprintf("client%d-value%d", clientID, i))
				err := client.Put(ctx, key, value)
				require.NoError(t, err)
			}
		}(clientID)
	}

	wg.Wait()

	// Build expected state: all keys from all clients
	expectedKeys := make(map[string]string)
	for clientID := 0; clientID < numClients; clientID++ {
		for i := 0; i < writesPerClient; i++ {
			key := fmt.Sprintf("client%d-key%d", clientID, i)
			value := fmt.Sprintf("client%d-value%d", clientID, i)
			expectedKeys[key] = value
		}
	}

	// Verify all clients eventually see all keys (eventual consistency)
	for clientID, client := range clients {
		for key, expectedValue := range expectedKeys {
			keyBytes := []byte(key)
			require.Eventually(t, func() bool {
				val, err := client.Get(ctx, keyBytes)
				if err != nil {
					return false
				}
				return string(val) == expectedValue
			}, 10*time.Second, 100*time.Millisecond,
				"client%d should eventually see key %s with value %s", clientID, key, expectedValue)
		}
	}
}

// TestMultiClient_DeletePropagation verifies that deletes propagate across clients.
func TestMultiClient_DeletePropagation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	const topic = "test-multi-client-delete"

	// Create two clients
	storage1, err := memdb.New()
	require.NoError(t, err)

	client1, err := kv.NewClient(ctx, topic, storage1,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client1.Close()

	storage2, err := memdb.New()
	require.NoError(t, err)

	client2, err := kv.NewClient(ctx, topic, storage2,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client2.Close()

	// Client 1 writes a key
	err = client1.Put(ctx, []byte("delete-test-key"), []byte("delete-test-value"))
	require.NoError(t, err)

	// Client 2 should eventually see it
	require.Eventually(t, func() bool {
		val, err := client2.Get(ctx, []byte("delete-test-key"))
		return err == nil && string(val) == "delete-test-value"
	}, 5*time.Second, 100*time.Millisecond)

	// Client 1 deletes the key
	err = client1.Delete(ctx, []byte("delete-test-key"))
	require.NoError(t, err)

	// Client 1 immediately sees the delete
	_, err = client1.Get(ctx, []byte("delete-test-key"))
	require.ErrorIs(t, err, kv.ErrNotFound)

	// Client 2 should eventually see the delete
	require.Eventually(t, func() bool {
		_, err := client2.Get(ctx, []byte("delete-test-key"))
		return err != nil && assert.ErrorIs(t, err, kv.ErrNotFound)
	}, 5*time.Second, 100*time.Millisecond, "client2 should eventually see the delete")
}

// TestSync_StressReadYourOwnWrites verifies the read-your-own-writes guarantee
// under heavy concurrent load. Each producer writes to its own key with
// monotonically increasing values. After each Put returns, the subsequent Get
// must return exactly what was written (since no other producer touches that key).
func TestSync_StressReadYourOwnWrites(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := t.Context()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-stress-ryow", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	const (
		numProducers      = 100
		writesPerProducer = 5000
	)

	var violations atomic.Int64
	var totalOps atomic.Int64
	var wg sync.WaitGroup

	for p := 0; p < numProducers; p++ {
		wg.Add(1)
		go func(producerID int) {
			defer wg.Done()

			// Each producer has its own key - no concurrent writes to same key
			key := []byte(fmt.Sprintf("producer-%d-key", producerID))

			for i := 0; i < writesPerProducer; i++ {
				// Monotonically increasing value for this producer
				writeValue := uint64(i + 1)

				// Encode as big-endian
				valueBuf := make([]byte, 8)
				binary.BigEndian.PutUint64(valueBuf, writeValue)

				// Write
				err := client.Put(ctx, key, valueBuf)
				if err != nil {
					t.Errorf("producer %d: Put failed: %v", producerID, err)
					return
				}

				// Read back - must see exactly what we wrote (no other writers)
				readVal, err := client.Get(ctx, key)
				if err != nil {
					t.Errorf("producer %d: Get failed after Put: %v", producerID, err)
					return
				}

				readValue := binary.BigEndian.Uint64(readVal)
				if readValue != writeValue {
					violations.Add(1)
					t.Errorf("RYOW violation: producer %d wrote %d, but read back %d",
						producerID, writeValue, readValue)
				}

				totalOps.Add(1)
			}
		}(p)
	}

	wg.Wait()

	t.Logf("Completed %d total operations across %d producers", totalOps.Load(), numProducers)
	t.Logf("Violations: %d", violations.Load())

	require.Zero(t, violations.Load(), "read-your-own-writes guarantee was violated")
}

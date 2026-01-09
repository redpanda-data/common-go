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
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"

	kv "github.com/redpanda-data/common-go/kvstore"
	"github.com/redpanda-data/common-go/kvstore/memdb"
)

func BenchmarkPut_Individual(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(b, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(b, err)

	storage, err := memdb.New()
	require.NoError(b, err)

	client, err := kv.NewClient(ctx, "bench-individual", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(b, err)
	defer client.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		key := []byte(fmt.Sprintf("key-%d", i))
		value := []byte(fmt.Sprintf("value-%d", i))
		err := client.Put(ctx, key, value)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPut_Batch100(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(b, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(b, err)

	storage, err := memdb.New()
	require.NoError(b, err)

	client, err := kv.NewClient(ctx, "bench-batch-100", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(b, err)
	defer client.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		batch := client.Batch()
		for j := 0; j < 100; j++ {
			key := []byte(fmt.Sprintf("key-%d-%d", i, j))
			value := []byte(fmt.Sprintf("value-%d-%d", i, j))
			batch.Put(key, value)
		}
		if err := batch.Execute(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPut_Batch1000(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(b, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(b, err)

	storage, err := memdb.New()
	require.NoError(b, err)

	client, err := kv.NewClient(ctx, "bench-batch-1000", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(b, err)
	defer client.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		batch := client.Batch()
		for j := 0; j < 1000; j++ {
			key := []byte(fmt.Sprintf("key-%d-%d", i, j))
			value := []byte(fmt.Sprintf("value-%d-%d", i, j))
			batch.Put(key, value)
		}
		if err := batch.Execute(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRestore measures how long NewClient takes to bootstrap from
// an existing topic with data. This simulates cold start / restore time.
func BenchmarkRestore(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	sizes := []int{
		10_000,
		100_000,
		1_000_000,
		10_000_000,
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("items_%d", size), func(b *testing.B) {
			benchmarkRestore(b, size)
		})
	}
}

// BenchmarkRestoreWithDuplicates measures restore time when compaction is lagging.
// Simulates updates to the same keys (e.g., 10 writes per key).
func BenchmarkRestoreWithDuplicates(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping integration benchmark")
	}

	// Test with 100k unique keys, but 10 writes each = 1M total records
	benchmarkRestoreWithDuplicates(b, 100_000, 10)
}

func benchmarkRestoreWithDuplicates(b *testing.B, uniqueKeys, writesPerKey int) {
	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(b, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(b, err)

	topic := fmt.Sprintf("bench-restore-dupes-%d-%d", uniqueKeys, writesPerKey)

	b.Logf("populating topic with %d unique keys, %d writes each (%d total records)...",
		uniqueKeys, writesPerKey, uniqueKeys*writesPerKey)

	storage, err := memdb.New()
	require.NoError(b, err)

	client, err := kv.NewClient(ctx, topic, storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(b, err)

	// Write each key multiple times (simulating updates before compaction)
	const batchSize = 10_000
	totalWrites := uniqueKeys * writesPerKey
	for i := 0; i < totalWrites; i += batchSize {
		batch := client.Batch()
		end := i + batchSize
		if end > totalWrites {
			end = totalWrites
		}
		for j := i; j < end; j++ {
			keyIdx := j % uniqueKeys
			writeNum := j / uniqueKeys
			key := []byte(fmt.Sprintf("key-%08d", keyIdx))
			value := []byte(fmt.Sprintf("value-%08d-v%d", keyIdx, writeNum))
			batch.Put(key, value)
		}
		if err := batch.Execute(ctx); err != nil {
			b.Fatal(err)
		}
	}

	client.Close()

	b.Log("population complete, starting restore benchmark")
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		freshStorage, err := memdb.New()
		require.NoError(b, err)

		newClient, err := kv.NewClient(ctx, topic, freshStorage,
			kv.WithBrokers(brokers),
		)
		require.NoError(b, err)
		newClient.Close()
	}
}

func benchmarkRestore(b *testing.B, numItems int) {
	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(b, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(b, err)

	topic := fmt.Sprintf("bench-restore-%d", numItems)

	// Populate topic with test data
	b.Logf("populating topic with %d items...", numItems)

	storage, err := memdb.New()
	require.NoError(b, err)

	client, err := kv.NewClient(ctx, topic, storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(b, err)

	// Write in batches of 10k for efficiency
	const batchSize = 10_000
	for i := 0; i < numItems; i += batchSize {
		batch := client.Batch()
		end := i + batchSize
		if end > numItems {
			end = numItems
		}
		for j := i; j < end; j++ {
			key := []byte(fmt.Sprintf("key-%08d", j))
			value := []byte(fmt.Sprintf("value-%08d", j))
			batch.Put(key, value)
		}
		if err := batch.Execute(ctx); err != nil {
			b.Fatal(err)
		}
	}

	client.Close()

	b.Log("population complete, starting restore benchmark")
	b.ResetTimer()

	// Benchmark: create new client with fresh storage (measures restore time)
	for i := 0; i < b.N; i++ {
		freshStorage, err := memdb.New()
		require.NoError(b, err)

		newClient, err := kv.NewClient(ctx, topic, freshStorage,
			kv.WithBrokers(brokers),
		)
		require.NoError(b, err)
		newClient.Close()
	}
}

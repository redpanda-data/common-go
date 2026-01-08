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
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/avast/retry-go/v5"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Client is a KV store backed by Kafka.
// Writes go through Kafka and block until visible in reads.
// Each topic should contain a single resource type.
type Client struct {
	kafka        *kgo.Client
	storage      Storage
	batchStorage BatchStorage // nil if storage doesn't support batching
	topic        string
	logger       *slog.Logger

	onSet    func(key, value []byte)
	onDelete func(key []byte)

	closed atomic.Bool

	consumerOffset int64         // protected by syncMu
	syncMu         sync.Mutex    // protects consumerOffset and syncBroadcast
	syncBroadcast  chan struct{} // closed when offset advances, replaced with new channel

	//nolint:containedctx // consumerCtx manages the consumer goroutine lifecycle, not request scope
	consumerCtx    context.Context
	consumerCancel context.CancelFunc
	consumerWg     sync.WaitGroup
}

// KeyValue represents a key-value pair.
type KeyValue struct {
	Key   []byte
	Value []byte
}

// ErrClientClosed is returned when operations are performed on a closed client.
var ErrClientClosed = errors.New("client is closed")

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("key not found")

// NewClient creates a new KV store client.
// Each topic should contain a single resource type with its own indexes.
//
// The provided context is used only for initialization (topic creation,
// metadata fetches, and bootstrap sync). It is not retained after NewClient
// returns - the client manages its own internal context for the consumer
// goroutine, which is cancelled when Close is called.
//
// The caller MUST call Close() to release resources when done with the client.
// Cancelling the provided context does not shut down the client.
//
// NewClient blocks until the consumer has caught up to the current high
// watermark, ensuring reads are consistent immediately after the client
// is created. For topics with large amounts of data, this may take some time.
func NewClient(ctx context.Context, topic string, storage Storage, opts ...ClientOption) (*Client, error) {
	cfg := &clientConfig{
		replicationFactor: -1,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	if len(cfg.brokers) == 0 {
		return nil, errors.New("at least one broker address is required (use WithBrokers)")
	}
	if topic == "" {
		return nil, errors.New("topic cannot be empty")
	}
	if storage == nil {
		return nil, errors.New("storage cannot be nil")
	}
	cfg.storage = storage

	// Default to slog.Default() if no logger provided
	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}

	kafkaOpts := []kgo.Opt{
		kgo.SeedBrokers(cfg.brokers...),
		kgo.ClientID("kvstore"),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			topic: {0: kgo.NewOffset().AtStart()},
		}),
		// Optimize for fast restore with large fetches
		kgo.FetchMaxBytes(100 * 1024 * 1024),          // 100MB per fetch (default 50MB)
		kgo.FetchMaxPartitionBytes(100 * 1024 * 1024), // 100MB per partition (default 1GB, but capped by FetchMaxBytes)
	}
	kafkaOpts = append(kafkaOpts, cfg.kafkaOptions...)

	kafka, err := kgo.NewClient(kafkaOpts...)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}

	if err := ensureTopicExists(ctx, kafka, topic, cfg.replicationFactor); err != nil {
		kafka.Close()
		return nil, err
	}

	// Get high watermark to know when bootstrap is complete.
	// We need to consume up to (highWatermark - 1) before reads are consistent.
	highWatermark, err := getHighWatermark(ctx, kafka, topic)
	if err != nil {
		kafka.Close()
		return nil, fmt.Errorf("get high watermark: %w", err)
	}

	consumerCtx, consumerCancel := context.WithCancel(context.Background())

	// Check if storage supports batch operations for faster restore
	batchStorage, ok := cfg.storage.(BatchStorage)
	if !ok {
		batchStorage = nil
	}

	c := &Client{
		kafka:          kafka,
		storage:        cfg.storage,
		batchStorage:   batchStorage,
		topic:          topic,
		logger:         cfg.logger,
		onSet:          cfg.onSet,
		onDelete:       cfg.onDelete,
		consumerCtx:    consumerCtx,
		consumerCancel: consumerCancel,
		consumerOffset: -1,
		syncBroadcast:  make(chan struct{}),
	}

	c.consumerWg.Add(1)
	go func() {
		defer c.consumerWg.Done()
		c.runConsumer()
	}()

	// Wait for consumer to catch up to high watermark before returning.
	// This ensures reads are consistent immediately after NewClient returns.
	if highWatermark > 0 {
		if err := c.sync(ctx, highWatermark-1); err != nil {
			c.Close()
			return nil, fmt.Errorf("bootstrap sync: %w", err)
		}
	}

	return c, nil
}

// Close shuts down the client.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}

	c.consumerCancel()
	c.consumerWg.Wait()
	c.kafka.Close()

	return nil
}

// Put stores a key-value pair. Blocks until the write is visible in this client's reads.
func (c *Client) Put(ctx context.Context, key []byte, value []byte) error {
	if c.closed.Load() {
		return ErrClientClosed
	}

	rec := &kgo.Record{
		Topic: c.topic,
		Key:   key,
		Value: value,
	}

	results := c.kafka.ProduceSync(ctx, rec)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("produce: %w", err)
	}

	return c.sync(ctx, results[0].Record.Offset)
}

// Get retrieves a value by key from local storage.
// Returns ErrNotFound if the key does not exist.
// May return stale data if this client's consumer is lagging or if other clients wrote recently.
func (c *Client) Get(ctx context.Context, key []byte) ([]byte, error) {
	if c.closed.Load() {
		return nil, ErrClientClosed
	}
	data, err := c.storage.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, ErrNotFound
	}
	return data, nil
}

// Delete removes a key. Blocks until the delete is visible in this client's reads.
func (c *Client) Delete(ctx context.Context, key []byte) error {
	if c.closed.Load() {
		return ErrClientClosed
	}

	rec := &kgo.Record{
		Topic: c.topic,
		Key:   key,
		Value: nil, // Tombstone
	}

	results := c.kafka.ProduceSync(ctx, rec)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("produce: %w", err)
	}

	return c.sync(ctx, results[0].Record.Offset)
}

// sync blocks until consumer has processed up to the given offset.
// If the consumer is experiencing transient errors, this will wait until
// it recovers or the context is cancelled.
func (c *Client) sync(ctx context.Context, offset int64) error {
	for {
		c.syncMu.Lock()
		if c.consumerOffset >= offset {
			c.syncMu.Unlock()
			return nil
		}
		if c.closed.Load() {
			c.syncMu.Unlock()
			return ErrClientClosed
		}
		// Capture current broadcast channel
		broadcast := c.syncBroadcast
		c.syncMu.Unlock()

		// Wait for offset advance or context cancellation
		select {
		case <-broadcast:
			// Consumer advanced offset, loop and check again
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Range iterates over keys in the given range from local storage.
// Use Prefix() to create a KeyConstraint for prefix queries.
// May return stale data if this client's consumer is lagging or if other clients wrote recently.
func (c *Client) Range(ctx context.Context, constraint KeyConstraint, opts QueryOptions) iter.Seq2[KeyValue, error] {
	return func(yield func(KeyValue, error) bool) {
		if c.closed.Load() {
			yield(KeyValue{}, ErrClientClosed)
			return
		}
		for kv, err := range c.storage.Range(ctx, constraint, opts) {
			if !yield(kv, err) {
				return
			}
		}
	}
}

func (c *Client) runConsumer() {
	defer func() {
		// Wake all waiters on shutdown
		c.syncMu.Lock()
		close(c.syncBroadcast)
		c.syncMu.Unlock()
	}()

	for {
		select {
		case <-c.consumerCtx.Done():
			return
		default:
		}

		fetches := c.kafka.PollFetches(c.consumerCtx)

		// Log errors but continue - franz-go handles reconnection internally
		for _, err := range fetches.Errors() {
			if errors.Is(err.Err, context.Canceled) {
				return
			}
			c.logger.Error("kvstore consumer poll error",
				"topic", c.topic,
				"error", err.Err,
			)
		}

		iter := fetches.RecordIter()
		var maxOffset int64
		var err error

		if c.batchStorage != nil {
			maxOffset, err = c.processFetchBatch(iter)
		} else {
			maxOffset, err = c.processFetchIndividual(iter)
		}

		if err != nil {
			return // context cancelled
		}

		if maxOffset >= 0 {
			c.syncMu.Lock()
			c.consumerOffset = maxOffset
			close(c.syncBroadcast)
			c.syncBroadcast = make(chan struct{})
			c.syncMu.Unlock()
		}
	}
}

// processFetchBatch processes records using batch storage with deduplication.
// Returns the max offset processed and any error.
func (c *Client) processFetchBatch(iter *kgo.FetchesRecordIter) (int64, error) {
	var maxOffset int64 = -1

	// Dedupe: only keep last value per key within this fetch
	seen := make(map[string]int) // key -> index in batch
	var batch []KeyValue
	for !iter.Done() {
		rec := iter.Next()
		key := string(rec.Key)
		if idx, ok := seen[key]; ok {
			batch[idx] = KeyValue{Key: rec.Key, Value: rec.Value}
		} else {
			seen[key] = len(batch)
			batch = append(batch, KeyValue{Key: rec.Key, Value: rec.Value})
		}
		if rec.Offset > maxOffset {
			maxOffset = rec.Offset
		}
	}

	if len(batch) == 0 {
		return maxOffset, nil
	}

	// Apply in chunks to avoid huge memdb transactions
	const chunkSize = 10_000
	for i := 0; i < len(batch); i += chunkSize {
		end := i + chunkSize
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[i:end]
		if err := c.retryStorageOp(c.consumerCtx, "batch", nil, maxOffset, func() error {
			return c.batchStorage.ApplyBatch(c.consumerCtx, chunk)
		}); err != nil {
			return -1, err
		}
	}

	// Fire callbacks after successful batch
	c.fireCallbacks(batch)

	return maxOffset, nil
}

// processFetchIndividual processes records one at a time.
// Returns the max offset processed and any error.
func (c *Client) processFetchIndividual(iter *kgo.FetchesRecordIter) (int64, error) {
	var maxOffset int64 = -1

	for !iter.Done() {
		rec := iter.Next()

		if rec.Value == nil {
			if err := c.retryStorageOp(c.consumerCtx, "delete", rec.Key, rec.Offset, func() error {
				return c.storage.Delete(c.consumerCtx, rec.Key)
			}); err != nil {
				return -1, err
			}
			if c.onDelete != nil {
				c.onDelete(rec.Key)
			}
		} else {
			if err := c.retryStorageOp(c.consumerCtx, "set", rec.Key, rec.Offset, func() error {
				return c.storage.Set(c.consumerCtx, rec.Key, rec.Value)
			}); err != nil {
				return -1, err
			}
			if c.onSet != nil {
				c.onSet(rec.Key, rec.Value)
			}
		}

		if rec.Offset > maxOffset {
			maxOffset = rec.Offset
		}
	}

	return maxOffset, nil
}

// fireCallbacks invokes onSet/onDelete callbacks for processed items.
func (c *Client) fireCallbacks(items []KeyValue) {
	for _, item := range items {
		if item.Value == nil {
			if c.onDelete != nil {
				c.onDelete(item.Key)
			}
		} else {
			if c.onSet != nil {
				c.onSet(item.Key, item.Value)
			}
		}
	}
}

// retryStorageOp executes fn with exponential backoff until success or context cancellation.
// Logs warnings on each retry attempt.
func (c *Client) retryStorageOp(ctx context.Context, operation string, key []byte, offset int64, fn func() error) error {
	attemptNum := uint(0)
	return retry.New(
		retry.Context(ctx),
		retry.Attempts(0), // Infinite retries
		retry.Delay(100*time.Millisecond),
		retry.MaxDelay(30*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			attemptNum = n + 1
			c.logger.Warn("kvstore storage operation failed, retrying",
				"topic", c.topic,
				"operation", operation,
				"key", string(key),
				"offset", offset,
				"attempt", attemptNum,
				"error", err,
			)
		}),
	).Do(fn)
}

// Batch creates a new batch for executing multiple operations atomically.
// The batch syncs only once after all operations complete, waiting for
// the highest offset.
func (c *Client) Batch() *Batch {
	return &Batch{
		client: c,
	}
}

// Batch accumulates Put and Delete operations for batch execution.
type Batch struct {
	client  *Client
	records []*kgo.Record
}

// Put adds a put operation to the batch.
func (b *Batch) Put(key []byte, value []byte) *Batch {
	b.records = append(b.records, &kgo.Record{
		Topic: b.client.topic,
		Key:   key,
		Value: value,
	})
	return b
}

// Delete adds a delete operation to the batch.
func (b *Batch) Delete(key []byte) *Batch {
	b.records = append(b.records, &kgo.Record{
		Topic: b.client.topic,
		Key:   key,
		Value: nil,
	})
	return b
}

// Execute produces all operations to Kafka and waits for the highest
// offset to be visible in this client's reads. Returns the first error encountered.
func (b *Batch) Execute(ctx context.Context) error {
	if b.client.closed.Load() {
		return ErrClientClosed
	}
	if len(b.records) == 0 {
		return nil
	}

	results := b.client.kafka.ProduceSync(ctx, b.records...)
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("produce batch: %w", err)
	}

	// Find highest offset
	var maxOffset int64 = -1
	for _, r := range results {
		if r.Record.Offset > maxOffset {
			maxOffset = r.Record.Offset
		}
	}

	return b.client.sync(ctx, maxOffset)
}

// getHighWatermark returns the high watermark (next offset to be written) for
// partition 0 of the topic. Returns 0 if the topic is empty.
func getHighWatermark(ctx context.Context, client *kgo.Client, topic string) (int64, error) {
	admin := kadm.NewClient(client)

	offsets, err := admin.ListEndOffsets(ctx, topic)
	if err != nil {
		return 0, fmt.Errorf("list end offsets: %w", err)
	}

	topicOffsets, ok := offsets[topic]
	if !ok {
		return 0, fmt.Errorf("topic %s not found in offsets response", topic)
	}

	partitionOffset, ok := topicOffsets[0]
	if !ok {
		return 0, fmt.Errorf("partition 0 not found for topic %s", topic)
	}

	if partitionOffset.Err != nil {
		return 0, fmt.Errorf("get offset for %s: %w", topic, partitionOffset.Err)
	}

	return partitionOffset.Offset, nil
}

func ensureTopicExists(ctx context.Context, client *kgo.Client, topic string, replicationFactor int16) error {
	admin := kadm.NewClient(client)

	// Enable log compaction for KV store semantics
	cleanupPolicy := "compact"
	configs := map[string]*string{
		"cleanup.policy": &cleanupPolicy,
	}

	resp, err := admin.CreateTopics(ctx, 1, replicationFactor, configs, topic)
	if err != nil {
		return fmt.Errorf("create topic %s: %w", topic, err)
	}

	for _, topicResp := range resp {
		if topicResp.Err != nil && !errors.Is(topicResp.Err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("create topic %s: %w", topicResp.Topic, topicResp.Err)
		}
	}

	metadata, err := admin.Metadata(ctx)
	if err != nil {
		return fmt.Errorf("fetch metadata for topic %s: %w", topic, err)
	}

	topicMeta, exists := metadata.Topics[topic]
	if !exists {
		return fmt.Errorf("topic %s does not exist after creation", topic)
	}

	if len(topicMeta.Partitions) != 1 {
		return fmt.Errorf("topic %s must have exactly 1 partition, has %d", topic, len(topicMeta.Partitions))
	}

	client.ForceMetadataRefresh()

	return nil
}

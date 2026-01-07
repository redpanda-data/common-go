package kvstore

import (
	"context"
	"iter"
)

// Serde handles serialization and deserialization of values.
type Serde[T any] interface {
	Serialize(T) ([]byte, error)
	Deserialize([]byte) (T, error)
}

// TypedKV is a typed key-value pair.
type TypedKV[T any] struct {
	Key   []byte
	Value T
}

// ResourceClient wraps Client with typed operations using a Serde.
type ResourceClient[T any] struct {
	client *Client
	serde  Serde[T]
}

// NewResourceClient creates a typed client wrapping the given bytes client.
func NewResourceClient[T any](client *Client, serde Serde[T]) *ResourceClient[T] {
	return &ResourceClient[T]{
		client: client,
		serde:  serde,
	}
}

// WrapOnSet creates a raw OnSet callback that deserializes values before calling the typed callback.
// Deserialization errors are silently ignored since hooks fire after successful storage operations,
// and the same serde is used for storage - a deserialization failure here indicates a bug.
func WrapOnSet[T any](serde Serde[T], fn func(key []byte, value T)) func(key, value []byte) {
	return func(key, value []byte) {
		val, err := serde.Deserialize(value)
		if err != nil {
			return
		}
		fn(key, val)
	}
}

// WrapOnDelete creates a typed OnDelete callback. This is a convenience wrapper for symmetry with WrapOnSet.
func WrapOnDelete[T any](fn func(key []byte)) func(key []byte) {
	return fn
}

// Raw returns the underlying untyped Client.
func (r *ResourceClient[T]) Raw() *Client {
	return r.client
}

// Put stores a value. Blocks until the write is visible in this client's reads.
func (r *ResourceClient[T]) Put(ctx context.Context, key []byte, value T) error {
	data, err := r.serde.Serialize(value)
	if err != nil {
		return err
	}
	return r.client.Put(ctx, key, data)
}

// Get retrieves a value by key from local storage.
// Returns ErrNotFound if the key does not exist.
// May return stale data if this client's consumer is lagging or if other clients wrote recently.
func (r *ResourceClient[T]) Get(ctx context.Context, key []byte) (T, error) {
	var zero T
	data, err := r.client.Get(ctx, key)
	if err != nil {
		return zero, err
	}
	return r.serde.Deserialize(data)
}

// Delete removes a key. Blocks until the delete is visible in this client's reads.
func (r *ResourceClient[T]) Delete(ctx context.Context, key []byte) error {
	return r.client.Delete(ctx, key)
}

// Range iterates over keys in the given range from local storage. Use Prefix() for prefix queries.
// May return stale data if this client's consumer is lagging or if other clients wrote recently.
// Stops iteration on first storage or deserialization error. Corrupt data indicates a real problem
// that must be fixed, not worked around.
func (r *ResourceClient[T]) Range(ctx context.Context, constraint KeyConstraint, opts QueryOptions) iter.Seq2[TypedKV[T], error] {
	return func(yield func(TypedKV[T], error) bool) {
		for kv, err := range r.client.Range(ctx, constraint, opts) {
			if err != nil {
				yield(TypedKV[T]{}, err)
				return
			}
			val, err := r.serde.Deserialize(kv.Value)
			if err != nil {
				yield(TypedKV[T]{}, err)
				return
			}
			if !yield(TypedKV[T]{Key: kv.Key, Value: val}, nil) {
				return
			}
		}
	}
}

// Batch creates a new typed batch for bulk operations.
func (r *ResourceClient[T]) Batch() *TypedBatch[T] {
	return &TypedBatch[T]{
		batch: r.client.Batch(),
		serde: r.serde,
	}
}

// TypedBatch accumulates typed Put and Delete operations.
type TypedBatch[T any] struct {
	batch *Batch
	serde Serde[T]
	err   error
}

// Put adds a put operation to the batch.
func (b *TypedBatch[T]) Put(key []byte, value T) *TypedBatch[T] {
	if b.err != nil {
		return b
	}
	data, err := b.serde.Serialize(value)
	if err != nil {
		b.err = err
		return b
	}
	b.batch.Put(key, data)
	return b
}

// Delete adds a delete operation to the batch.
func (b *TypedBatch[T]) Delete(key []byte) *TypedBatch[T] {
	b.batch.Delete(key)
	return b
}

// Execute produces all operations and waits for visibility in this client's reads.
func (b *TypedBatch[T]) Execute(ctx context.Context) error {
	if b.err != nil {
		return b.err
	}
	return b.batch.Execute(ctx)
}

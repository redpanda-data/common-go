package memdb

import (
	"context"
	"fmt"
	"iter"

	"github.com/hashicorp/go-memdb"

	kv "github.com/redpanda-data/common-go/kvstore"
)

// entry is stored in go-memdb.
type entry struct {
	Key   string
	Value []byte
}

// Storage provides in-memory storage backed by go-memdb.
type Storage struct {
	db *memdb.MemDB
}

// New creates a new in-memory storage.
func New() (*Storage, error) {
	schema := &memdb.DBSchema{
		Tables: map[string]*memdb.TableSchema{
			"kv": {
				Name: "kv",
				Indexes: map[string]*memdb.IndexSchema{
					"id": {
						Name:    "id",
						Unique:  true,
						Indexer: &memdb.StringFieldIndex{Field: "Key"},
					},
				},
			},
		},
	}

	db, err := memdb.NewMemDB(schema)
	if err != nil {
		return nil, fmt.Errorf("create memdb: %w", err)
	}

	return &Storage{
		db: db,
	}, nil
}

// Get retrieves a value by key.
func (s *Storage) Get(ctx context.Context, key []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	txn := s.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First("kv", "id", string(key))
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	e, ok := raw.(*entry)
	if !ok {
		return nil, fmt.Errorf("unexpected type: %T", raw)
	}
	return e.Value, nil
}

// Set stores a key-value pair.
func (s *Storage) Set(ctx context.Context, key []byte, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	txn := s.db.Txn(true)
	defer txn.Abort()

	if err := txn.Insert("kv", &entry{Key: string(key), Value: value}); err != nil {
		return err
	}

	txn.Commit()
	return nil
}

// Delete removes a key.
func (s *Storage) Delete(ctx context.Context, key []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	txn := s.db.Txn(true)
	defer txn.Abort()

	if _, err := txn.DeleteAll("kv", "id", string(key)); err != nil {
		return err
	}

	txn.Commit()
	return nil
}

// ApplyBatch applies a batch of set and delete operations in a single transaction.
// For each item: nil Value means delete, non-nil means set.
func (s *Storage) ApplyBatch(ctx context.Context, items []kv.KeyValue) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	txn := s.db.Txn(true)
	defer txn.Abort()

	for _, item := range items {
		if item.Value == nil {
			// Delete
			if _, err := txn.DeleteAll("kv", "id", string(item.Key)); err != nil {
				return err
			}
		} else {
			// Set
			if err := txn.Insert("kv", &entry{Key: string(item.Key), Value: item.Value}); err != nil {
				return err
			}
		}
	}

	txn.Commit()
	return nil
}

// Range iterates over keys in the given range.
func (s *Storage) Range(ctx context.Context, c kv.KeyConstraint, opts kv.QueryOptions) iter.Seq2[kv.KeyValue, error] {
	return func(yield func(kv.KeyValue, error) bool) {
		if err := ctx.Err(); err != nil {
			yield(kv.KeyValue{}, err)
			return
		}

		txn := s.db.Txn(false)
		defer txn.Abort()

		var it memdb.ResultIterator
		var err error

		if len(c.Start) > 0 {
			it, err = txn.LowerBound("kv", "id", string(c.Start))
		} else {
			it, err = txn.Get("kv", "id")
		}
		if err != nil {
			yield(kv.KeyValue{}, fmt.Errorf("range query: %w", err))
			return
		}

		count := 0
		for obj := it.Next(); obj != nil && (opts.Limit <= 0 || count < opts.Limit); obj = it.Next() {
			e, ok := obj.(*entry)
			if !ok {
				yield(kv.KeyValue{}, fmt.Errorf("unexpected type: %T", obj))
				return
			}
			// Check upper bound (exclusive)
			if len(c.End) > 0 && e.Key >= string(c.End) {
				break
			}
			if !yield(kv.KeyValue{Key: []byte(e.Key), Value: e.Value}, nil) {
				return
			}
			count++
		}
	}
}

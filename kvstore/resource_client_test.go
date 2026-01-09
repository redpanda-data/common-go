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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"

	kv "github.com/redpanda-data/common-go/kvstore"
	"github.com/redpanda-data/common-go/kvstore/memdb"
)

type user struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

func TestResourceClient_PutGet(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-resource-putget", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	users := kv.NewResourceClient[user](client, kv.JSON[user]())

	// Put
	err = users.Put(ctx, []byte("user:1"), user{Email: "alice@example.com", Name: "Alice"})
	require.NoError(t, err)

	// Get
	u, err := users.Get(ctx, []byte("user:1"))
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.Equal(t, "Alice", u.Name)

	// Get non-existent returns ErrNotFound
	u, err = users.Get(ctx, []byte("user:nonexistent"))
	require.ErrorIs(t, err, kv.ErrNotFound)
	assert.Equal(t, user{}, u)
}

func TestResourceClient_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-resource-delete", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	users := kv.NewResourceClient[user](client, kv.JSON[user]())

	// Put then delete
	err = users.Put(ctx, []byte("user:1"), user{Email: "alice@example.com", Name: "Alice"})
	require.NoError(t, err)

	err = users.Delete(ctx, []byte("user:1"))
	require.NoError(t, err)

	// Verify deleted - should return ErrNotFound
	u, err := users.Get(ctx, []byte("user:1"))
	require.ErrorIs(t, err, kv.ErrNotFound)
	assert.Equal(t, user{}, u)
}

func TestResourceClient_Range(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-resource-range", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	users := kv.NewResourceClient[user](client, kv.JSON[user]())

	// Put multiple users
	for i := 0; i < 10; i++ {
		err = users.Put(ctx, []byte(fmt.Sprintf("user:%d", i)), user{
			Email: fmt.Sprintf("user%d@example.com", i),
			Name:  fmt.Sprintf("User%d", i),
		})
		require.NoError(t, err)
	}

	// Range all
	var results []kv.TypedKV[user]
	for item, err := range users.Range(ctx, kv.Prefix("user:"), kv.QueryOptions{Limit: 100}) {
		require.NoError(t, err)
		results = append(results, item)
	}
	assert.Len(t, results, 10)

	// Range with limit
	results = nil
	for item, err := range users.Range(ctx, kv.Prefix("user:"), kv.QueryOptions{Limit: 5}) {
		require.NoError(t, err)
		results = append(results, item)
	}
	assert.Len(t, results, 5)
}

func TestResourceClient_Batch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-resource-batch", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	users := kv.NewResourceClient[user](client, kv.JSON[user]())

	// Batch put
	err = users.Batch().
		Put([]byte("user:1"), user{Email: "a@example.com", Name: "A"}).
		Put([]byte("user:2"), user{Email: "b@example.com", Name: "B"}).
		Put([]byte("user:3"), user{Email: "c@example.com", Name: "C"}).
		Execute(ctx)
	require.NoError(t, err)

	// Verify all written
	for i := 1; i <= 3; i++ {
		u, err := users.Get(ctx, []byte(fmt.Sprintf("user:%d", i)))
		require.NoError(t, err)
		assert.NotEqual(t, user{}, u)
	}

	// Batch delete
	err = users.Batch().
		Delete([]byte("user:1")).
		Delete([]byte("user:2")).
		Execute(ctx)
	require.NoError(t, err)

	// Verify deleted - should return ErrNotFound
	u, err := users.Get(ctx, []byte("user:1"))
	require.ErrorIs(t, err, kv.ErrNotFound)
	assert.Equal(t, user{}, u)

	u, err = users.Get(ctx, []byte("user:2"))
	require.ErrorIs(t, err, kv.ErrNotFound)
	assert.Equal(t, user{}, u)

	// user:3 should still exist
	u, err = users.Get(ctx, []byte("user:3"))
	require.NoError(t, err)
	assert.Equal(t, "c@example.com", u.Email)
}

func TestResourceClient_BatchMixed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()

	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	defer container.Terminate(ctx)

	brokers, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)

	storage, err := memdb.New()
	require.NoError(t, err)

	client, err := kv.NewClient(ctx, "test-resource-batch-mixed", storage,
		kv.WithBrokers(brokers),
	)
	require.NoError(t, err)
	defer client.Close()

	users := kv.NewResourceClient[user](client, kv.JSON[user]())

	// First put some users
	err = users.Put(ctx, []byte("user:old"), user{Email: "old@example.com", Name: "Old"})
	require.NoError(t, err)

	// Mixed batch: puts and deletes
	err = users.Batch().
		Put([]byte("user:new1"), user{Email: "new1@example.com", Name: "New1"}).
		Delete([]byte("user:old")).
		Put([]byte("user:new2"), user{Email: "new2@example.com", Name: "New2"}).
		Execute(ctx)
	require.NoError(t, err)

	// Verify deleted - should return ErrNotFound
	u, err := users.Get(ctx, []byte("user:old"))
	require.ErrorIs(t, err, kv.ErrNotFound)
	assert.Equal(t, user{}, u)

	u, err = users.Get(ctx, []byte("user:new1"))
	require.NoError(t, err)
	assert.Equal(t, "new1@example.com", u.Email)

	u, err = users.Get(ctx, []byte("user:new2"))
	require.NoError(t, err)
	assert.Equal(t, "new2@example.com", u.Email)
}

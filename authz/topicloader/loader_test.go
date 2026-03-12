// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package topicloader_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/topicloader"
)

const testTopic = "_redpanda.policy-sync"

// startRedpanda starts a Redpanda container and returns the broker address.
// Skips the test if running in short mode.
func startRedpanda(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test: requires Docker")
	}
	ctx := context.Background()
	container, err := redpanda.Run(ctx, "redpandadata/redpanda:latest",
		redpanda.WithAutoCreateTopics(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	broker, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err)
	return broker
}

// produce writes a record to the given topic using a one-shot producer.
func produce(t *testing.T, broker, topic string, value []byte) {
	t.Helper()
	ctx := context.Background()
	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	require.NoError(t, err)
	defer client.Close()
	res := client.ProduceSync(ctx, &kgo.Record{Topic: topic, Value: value})
	require.NoError(t, res.FirstErr())
}

// startWatcher launches WatchPolicyFromTopic in a goroutine and waits briefly
// for the consumer to connect and reach the end of the topic before returning.
func startWatcher(t *testing.T, ctx context.Context, broker, group string, cb func(authz.Policy, error)) chan error {
	t.Helper()
	cfg := topicloader.Config{
		Brokers:       []string{broker},
		Topic:         testTopic,
		ConsumerGroup: group,
	}
	done := make(chan error, 1)
	go func() {
		done <- topicloader.WatchPolicyFromTopic(ctx, cfg, cb)
	}()
	// Allow time for the consumer to join the group and seek to AtEnd.
	time.Sleep(time.Second)
	return done
}

func TestWatchPolicyFromTopic_HappyPath(t *testing.T) {
	broker := startRedpanda(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	want := authz.Policy{
		Roles: []authz.Role{
			{ID: "admin", Permissions: []authz.PermissionName{"read", "write"}},
		},
		Bindings: []authz.RoleBinding{
			{Role: "admin", Principal: "User:alice@example.com"},
		},
	}

	got := make(chan authz.Policy, 1)
	done := startWatcher(t, ctx, broker, "test-happy-path", func(p authz.Policy, err error) {
		if err != nil {
			return
		}
		select {
		case got <- p:
		default:
		}
	})

	data, err := json.Marshal(want)
	require.NoError(t, err)
	produce(t, broker, testTopic, data)

	select {
	case p := <-got:
		assert.Equal(t, want, p)
		cancel()
	case <-ctx.Done():
		t.Fatal("timed out waiting for policy callback")
	}

	require.NoError(t, <-done)
}

func TestWatchPolicyFromTopic_Update(t *testing.T) {
	broker := startRedpanda(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	policy1 := authz.Policy{
		Roles: []authz.Role{{ID: "viewer", Permissions: []authz.PermissionName{"read"}}},
	}
	policy2 := authz.Policy{
		Roles: []authz.Role{{ID: "editor", Permissions: []authz.PermissionName{"read", "write"}}},
	}

	received := make(chan authz.Policy, 10)
	done := startWatcher(t, ctx, broker, "test-update", func(p authz.Policy, err error) {
		if err != nil {
			return
		}
		received <- p
	})

	data1, err := json.Marshal(policy1)
	require.NoError(t, err)
	data2, err := json.Marshal(policy2)
	require.NoError(t, err)
	produce(t, broker, testTopic, data1)
	produce(t, broker, testTopic, data2)

	var got []authz.Policy
	for len(got) < 2 {
		select {
		case p := <-received:
			got = append(got, p)
		case <-ctx.Done():
			t.Fatalf("timed out: only received %d records", len(got))
		}
	}
	cancel()

	assert.Equal(t, policy1, got[0])
	assert.Equal(t, policy2, got[1])
	require.NoError(t, <-done)
}

func TestWatchPolicyFromTopic_BadJSON(t *testing.T) {
	broker := startRedpanda(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	valid := authz.Policy{
		Roles: []authz.Role{{ID: "reader", Permissions: []authz.PermissionName{"read"}}},
	}

	var gotErr error
	gotPolicy := make(chan authz.Policy, 1)
	done := startWatcher(t, ctx, broker, "test-bad-json", func(p authz.Policy, err error) {
		if err != nil {
			gotErr = err
			return
		}
		select {
		case gotPolicy <- p:
		default:
		}
	})

	produce(t, broker, testTopic, []byte("not-valid-json"))
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	produce(t, broker, testTopic, data)

	select {
	case p := <-gotPolicy:
		assert.NotNil(t, gotErr, "expected an error for bad JSON record")
		assert.Equal(t, valid, p)
		cancel()
	case <-ctx.Done():
		t.Fatal("timed out waiting for valid policy after bad JSON")
	}

	require.NoError(t, <-done)
}

func TestWatchPolicyFromTopic_CtxCancel(t *testing.T) {
	broker := startRedpanda(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := startWatcher(t, ctx, broker, "test-ctx-cancel", func(_ authz.Policy, _ error) {})
	cancel()

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("WatchPolicyFromTopic did not return after ctx cancel")
	}
}

func TestWatchPolicyFromTopic_BadBroker(t *testing.T) {
	cfg := topicloader.Config{
		Brokers:       []string{"localhost:1"},
		Topic:         testTopic,
		ConsumerGroup: "test-bad-broker",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := topicloader.WatchPolicyFromTopic(ctx, cfg, func(_ authz.Policy, _ error) {})
	require.NoError(t, err)
}

// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package rpadmin

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// testRedpandaImage tracks the latest 26.1.x nightly so the
// pre/post-restart probe endpoints (introduced in 25.1) plus any
// in-development changes to them are covered.
const testRedpandaImage = "redpandadata/redpanda-nightly:latest"

// startTestBroker spins up a single-node Redpanda container and returns
// an AdminAPI client pointed at it plus a kadm client wired to the same
// broker. The container is terminated on test cleanup.
func startTestBroker(t *testing.T) (*AdminAPI, *kadm.Client) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	ctx := t.Context()
	container, err := redpanda.Run(ctx, testRedpandaImage)
	require.NoError(t, err, "start redpanda container")
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate redpanda container: %v", err)
		}
	})

	adminURL, err := container.AdminAPIAddress(ctx)
	require.NoError(t, err, "get admin API address")

	adminClient, err := NewAdminAPI([]string{adminURL}, new(NopAuth), nil)
	require.NoError(t, err, "build AdminAPI client")

	seed, err := container.KafkaSeedBroker(ctx)
	require.NoError(t, err, "get kafka seed broker")

	kClient, err := kgo.NewClient(kgo.SeedBrokers(seed))
	require.NoError(t, err, "build kgo client")
	t.Cleanup(kClient.Close)

	return adminClient, kadm.NewClient(kClient)
}

// TestPreRestartProbe spins up a real Redpanda broker, creates an RF=1
// topic with multiple partitions, and verifies that those partitions
// show up in rf1_offline. This pins the endpoint path, the JSON tag
// names, and the limit query-param wire contract against a real broker.
func TestPreRestartProbe(t *testing.T) {
	adminClient, kadmClient := startTestBroker(t)
	ctx := t.Context()

	const (
		topic         = "rpadmin-prerestart-probe"
		numPartitions = 4
	)
	_, err := kadmClient.CreateTopic(ctx, numPartitions, 1, nil, topic)
	require.NoError(t, err, "create RF=1 topic")

	want := []string{
		"kafka/" + topic + "/0",
		"kafka/" + topic + "/1",
		"kafka/" + topic + "/2",
		"kafka/" + topic + "/3",
	}

	// Probe is computed from the broker's local view, which may briefly
	// trail topic creation. Poll until every partition appears.
	var got PreRestartCheckResult
	require.Eventually(t, func() bool {
		var err error
		got, err = adminClient.PreRestartProbe(ctx, 0)
		if err != nil {
			t.Logf("pre-restart probe error: %v", err)
			return false
		}
		for _, p := range want {
			if !slices.Contains(got.Risks.RF1Offline, p) {
				return false
			}
		}
		return true
	}, 30*time.Second, 500*time.Millisecond, "all RF=1 partitions never appeared in rf1_offline")

	// The other three categories must at least decode successfully so a
	// silent JSON-tag drift between client and server becomes a test
	// failure. They are legitimately allowed to be empty.
	assert.NotNil(t, got.Risks.FullAcksProduceUnavailable)
	assert.NotNil(t, got.Risks.Unavailable)
	assert.NotNil(t, got.Risks.Acks1DataLoss)

	// limit caps the response size per category. With our four RF=1
	// partitions plus any internal partitions, an unbounded probe will
	// always return ≥1 entry — passing limit=1 must trim that to ≤1.
	limited, err := adminClient.PreRestartProbe(ctx, 1)
	require.NoError(t, err)
	require.NotEmpty(t, got.Risks.RF1Offline, "precondition: unbounded probe returned partitions")
	assert.LessOrEqual(t, len(limited.Risks.RF1Offline), 1, "limit=1 did not cap rf1_offline")
	assert.LessOrEqual(t, len(limited.Risks.FullAcksProduceUnavailable), 1)
	assert.LessOrEqual(t, len(limited.Risks.Unavailable), 1)
	assert.LessOrEqual(t, len(limited.Risks.Acks1DataLoss), 1)
}

// TestPostRestartProbe spins up a real Redpanda broker and verifies the
// post-restart probe responds with a valid load_reclaimed_pc and that
// the limit query param is accepted.
func TestPostRestartProbe(t *testing.T) {
	adminClient, _ := startTestBroker(t)
	ctx := t.Context()

	var got PostRestartCheckResult
	require.Eventually(t, func() bool {
		var err error
		got, err = adminClient.PostRestartProbe(ctx, 0)
		if err != nil {
			t.Logf("post-restart probe error: %v", err)
			return false
		}
		return true
	}, 30*time.Second, 500*time.Millisecond, "post-restart probe never succeeded")

	assert.GreaterOrEqual(t, got.LoadReclaimedPercent, 0)
	assert.LessOrEqual(t, got.LoadReclaimedPercent, 100)

	// limit propagates without the broker rejecting the request.
	_, err := adminClient.PostRestartProbe(ctx, 64)
	require.NoError(t, err)
}

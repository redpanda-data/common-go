// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package loader_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/loader"
)

func TestWatchBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
roles:
- id: Writer
  permissions:
  - p1
bindings:
- principal: "User:alice@example.com"
  role: Writer
  scope: "orgs/o1"
`), 0o644))

	ch := make(chan authz.Policy, 10)
	policy, unwatch, err := loader.WatchPolicyFile(path, func(p authz.Policy, err error) {
		t.Logf("callback: bindings=%d err=%v", len(p.Bindings), err)
		if err == nil {
			ch <- p
		}
	})
	require.NoError(t, err)
	defer unwatch()
	require.Len(t, policy.Bindings, 1)

	t.Log("writing updated policy")
	require.NoError(t, os.WriteFile(path, []byte(`
roles:
- id: Writer
  permissions:
  - p1
bindings:
- principal: "User:alice@example.com"
  role: Writer
  scope: "orgs/o1"
- principal: "User:bob@example.com"
  role: Writer
  scope: "orgs/o1"
`), 0o644))

	select {
	case p := <-ch:
		require.Len(t, p.Bindings, 2)
		t.Log("got updated policy")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for callback")
	}
}

func TestWatchUnwatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")

	writeTestPolicy(t, path, 1)

	var calls atomic.Int32
	policy, unwatch, err := loader.WatchPolicyFile(path, func(p authz.Policy, err error) {
		calls.Add(1)
	})
	require.NoError(t, err)
	require.Len(t, policy.Bindings, 1)

	// Verify watcher is alive: write and expect callback.
	writeTestPolicy(t, path, 2)
	time.Sleep(500 * time.Millisecond)
	require.Greater(t, calls.Load(), int32(0), "expected at least one callback before unwatch")

	// Unwatch and reset counter.
	require.NoError(t, unwatch())
	calls.Store(0)

	// Write again — callback should NOT fire.
	writeTestPolicy(t, path, 3)
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(0), calls.Load(), "callback should not fire after unwatch")
}

func writeTestPolicy(t *testing.T, path string, nBindings int) {
	t.Helper()
	var bindings string
	for i := range nBindings {
		bindings += fmt.Sprintf("- principal: \"User:user%d@example.com\"\n  role: Writer\n  scope: \"orgs/o1\"\n", i)
	}
	data := fmt.Sprintf("roles:\n- id: Writer\n  permissions:\n  - p1\nbindings:\n%s", bindings)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

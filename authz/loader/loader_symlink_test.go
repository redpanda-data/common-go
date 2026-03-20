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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/loader"
)

// TestWatchPolicyFile_SymlinkSwap reproduces the Kubernetes ConfigMap update
// pattern: the kubelet atomically swaps a symlink to point to a new directory.
// During the swap the old symlink target is briefly removed before the new one
// appears. The file watcher must survive this transient removal and pick up
// the new content.
func TestWatchPolicyFile_SymlinkSwap(t *testing.T) {
	dir := t.TempDir()

	gen1 := filepath.Join(dir, "gen1")
	require.NoError(t, os.Mkdir(gen1, 0o755))
	writePolicy(t, filepath.Join(gen1, "policy.yaml"), 1)

	dataLink := filepath.Join(dir, "..data")
	require.NoError(t, os.Symlink(gen1, dataLink))

	policyPath := filepath.Join(dir, "policy.yaml")
	require.NoError(t, os.Symlink(filepath.Join(dataLink, "policy.yaml"), policyPath))

	reloadCh := make(chan authz.Policy, 10)
	errCh := make(chan error, 10)

	policy, _, err := loader.WatchPolicyFile(policyPath, func(p authz.Policy, err error) {
		if err != nil {
			errCh <- err
			return
		}
		reloadCh <- p
	})
	require.NoError(t, err)
	require.Len(t, policy.Bindings, 1)

	// First symlink swap.
	swapSymlink(t, dir, dataLink, 2)

	select {
	case p := <-reloadCh:
		assert.Len(t, p.Bindings, 2, "expected 2 bindings after first swap")
	case err := <-errCh:
		t.Fatalf("watcher returned error instead of reloading: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for policy reload after first symlink swap")
	}

	// Second swap: verify the watcher survived the first swap.
	swapSymlink(t, dir, dataLink, 3)

	select {
	case p := <-reloadCh:
		assert.Len(t, p.Bindings, 3, "expected 3 bindings after second swap")
	case err := <-errCh:
		t.Fatalf("watcher died after first swap: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("watcher is dead -- timed out waiting for second policy reload")
	}
}

func swapSymlink(t *testing.T, dir, dataLink string, nBindings int) {
	t.Helper()
	gen := filepath.Join(dir, fmt.Sprintf("gen%d", nBindings))
	require.NoError(t, os.Mkdir(gen, 0o755))
	writePolicy(t, filepath.Join(gen, "policy.yaml"), nBindings)

	require.NoError(t, os.Remove(dataLink))
	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.Symlink(gen, dataLink))
}

func writePolicy(t *testing.T, path string, nBindings int) {
	t.Helper()
	var bindings string
	for i := range nBindings {
		bindings += fmt.Sprintf("- principal: \"User:user%d@example.com\"\n  role: Writer\n  scope: \"organizations/org1/resourcegroups/rg1/dataplanes/dp1\"\n", i)
	}
	data := fmt.Sprintf("roles:\n- id: Writer\n  permissions:\n  - dataplane_pipeline_create\nbindings:\n%s", bindings)
	require.NoError(t, os.WriteFile(path, []byte(data), 0o644))
}

package loader_test

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/redpanda-data/common-go/authz"
	"github.com/redpanda-data/common-go/authz/loader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWatchPolicyFile_AtomicRename reproduces the neovim atomic save pattern
// on Linux: write to a temp file in the same directory, then rename over the
// original. This causes inotify to lose the watch because the inode changes.
//
// This test currently fails -- the watcher does not pick up the new content.
func TestWatchPolicyFile_AtomicRename(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.yaml")

	initialPolicy := []byte(`roles:
  - id: reader
    permissions:
      - read
bindings:
  - role: reader
    principal: "User:alice@example.com"
    scope: "organizations/test"
`)
	require.NoError(t, os.WriteFile(policyPath, initialPolicy, 0o644))

	var mu sync.Mutex
	var reloadedPolicy *authz.Policy
	var reloadErr error

	policy, unwatch, err := loader.WatchPolicyFile(policyPath, func(p authz.Policy, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			reloadErr = err
		} else {
			reloadedPolicy = &p
		}
	})
	require.NoError(t, err)
	defer func() { _ = unwatch() }()

	// Verify initial load
	require.Len(t, policy.Roles, 1)
	assert.Equal(t, "reader", string(policy.Roles[0].ID))

	// Simulate neovim atomic save: write temp file, rename over original.
	// This is exactly what neovim does with 'backupcopy=no' (the default on Linux).
	updatedPolicy := []byte(`roles:
  - id: admin
    permissions:
      - read
      - write
      - delete
bindings:
  - role: admin
    principal: "User:alice@example.com"
    scope: "organizations/test"
`)

	// Neovim with backupcopy=no (default on Linux):
	// 1. Rename original to backup (original inode moves away)
	// 2. Write new content to original path (new inode)
	// 3. Unlink backup
	backupPath := policyPath + "~"
	require.NoError(t, os.Rename(policyPath, backupPath))
	require.NoError(t, os.WriteFile(policyPath, updatedPolicy, 0o644))
	require.NoError(t, os.Remove(backupPath))

	// Wait for first reload
	waitForReload := func(t *testing.T, expected string, timeout time.Duration) {
		t.Helper()
		deadline := time.After(timeout)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-deadline:
				mu.Lock()
				e := reloadErr
				mu.Unlock()
				if e != nil {
					t.Fatalf("watcher error: %v", e)
				}
				t.Fatalf("timed out waiting for role %q", expected)
			case <-ticker.C:
				mu.Lock()
				got := reloadedPolicy
				reloadedPolicy = nil // reset for next round
				mu.Unlock()
				if got != nil {
					require.Len(t, got.Roles, 1)
					assert.Equal(t, expected, string(got.Roles[0].ID))
					return
				}
			}
		}
	}

	waitForReload(t, "admin", 3*time.Second)

	// Second neovim edit -- this is where the watcher typically breaks.
	secondPolicy := []byte(`roles:
  - id: superadmin
    permissions:
      - everything
bindings:
  - role: superadmin
    principal: "User:alice@example.com"
    scope: "organizations/test"
`)
	backupPath2 := policyPath + "~"
	require.NoError(t, os.Rename(policyPath, backupPath2))
	require.NoError(t, os.WriteFile(policyPath, secondPolicy, 0o644))
	require.NoError(t, os.Remove(backupPath2))

	waitForReload(t, "superadmin", 3*time.Second)
}

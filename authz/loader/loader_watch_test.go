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
	fmt.Println("before")
	policy, unwatch, err := loader.WatchPolicyFile(path, func(p authz.Policy, err error) {
		t.Logf("callback: bindings=%d err=%v", len(p.Bindings), err)
		if err == nil {
			ch <- p
		}
	})

	fmt.Println("after")
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

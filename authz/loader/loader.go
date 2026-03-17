// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package loader

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"

	"github.com/redpanda-data/common-go/authz"
)

// LoadPolicyFromFile loads a Policy from a YAML file using koanf.
func LoadPolicyFromFile(path string) (authz.Policy, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return authz.Policy{}, fmt.Errorf("failed to load policy file %s: %w", path, err)
	}
	var policy authz.Policy
	if err := k.Unmarshal("", &policy); err != nil {
		return authz.Policy{}, fmt.Errorf("failed to unmarshal policy: %w", err)
	}

	return policy, nil
}

// LoadPolicyFromBytes loads a Policy from YAML bytes using koanf.
func LoadPolicyFromBytes(data []byte) (authz.Policy, error) {
	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(data), yaml.Parser()); err != nil {
		return authz.Policy{}, fmt.Errorf("failed to load policy data: %w", err)
	}
	var policy authz.Policy
	if err := k.Unmarshal("", &policy); err != nil {
		return authz.Policy{}, fmt.Errorf("failed to unmarshal policy: %w", err)
	}
	return policy, nil
}

// PolicyCallback is called when a policy is loaded or reloaded.
// If an error occurs during loading, policy will be empty and err will be set.
type PolicyCallback func(policy authz.Policy, err error)

// PolicyUnwatch stops watching the policy file for changes.
type PolicyUnwatch func() error

// InitializeWatchError is the error when watching the policy file is unable to be
// initialized.
type InitializeWatchError struct {
	Err error
}

// Error implements the error interface for InitializeWatchError.
func (e *InitializeWatchError) Error() string {
	return fmt.Sprintf("unable to initialize watch: %v", e.Err)
}

// Unwrap returns the wrapped error, allowing errors.Is and errors.As to work.
func (e *InitializeWatchError) Unwrap() error {
	return e.Err
}

// WatchPolicyFile watches a YAML policy file for changes and calls the callback
// when the file is modified. This is particularly useful for Kubernetes ConfigMap
// mounted files which are updated via symlink changes.
//
// Koanf's file watcher exits on transient errors (e.g. symlink briefly removed
// during a ConfigMap update). This function runs the watcher in a loop,
// restarting it automatically when it exits. Call the returned PolicyUnwatch
// to stop the loop and clean up.
func WatchPolicyFile(path string, callback PolicyCallback) (authz.Policy, PolicyUnwatch, error) {
	policy, err := LoadPolicyFromFile(path)
	if err != nil {
		return authz.Policy{}, nil, fmt.Errorf("failed to load policy file %s: %w", path, err)
	}

	// restartCh is signalled by the watch callback when koanf's watcher exits.
	restartCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})

	watchCb := func(_ any, watchErr error) {
		if watchErr != nil {
			slog.Warn("Policy file watcher exited, will restart",
				"path", path, "error", watchErr)
			select {
			case restartCh <- struct{}{}:
			default:
			}
			return
		}
		p, err := LoadPolicyFromFile(path)
		if err != nil {
			callback(authz.Policy{}, fmt.Errorf("failed to reload policy file %s: %w", path, err))
			return
		}
		callback(p, nil)
	}

	// Initial watch.
	fp := file.Provider(path)
	if err := fp.Watch(watchCb); err != nil {
		return authz.Policy{}, nil, &InitializeWatchError{Err: err}
	}

	// Restart loop: waits for the watcher to die, then restarts it.
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-restartCh:
			}

			// Backoff to avoid busy-looping if the file is persistently gone.
			select {
			case <-stopCh:
				return
			case <-time.After(time.Second):
			}

			slog.Info("Restarting policy file watcher", "path", path)
			fp := file.Provider(path)
			if err := fp.Watch(watchCb); err != nil {
				slog.Error("Failed to restart policy file watcher",
					"path", path, "error", err)
				// Signal ourselves to retry.
				select {
				case restartCh <- struct{}{}:
				default:
				}
				continue
			}

			// Reload immediately -- we may have missed updates while dead.
			if p, err := LoadPolicyFromFile(path); err == nil {
				callback(p, nil)
			}
		}
	}()

	unwatch := func() error {
		close(stopCh)
		return nil
	}

	return policy, unwatch, nil
}

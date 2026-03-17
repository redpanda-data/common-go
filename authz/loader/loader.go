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

	fp := file.Provider(path)
	stopCh := make(chan struct{})
	diedCh := make(chan struct{}, 1)

	watchFunc := func(_ any, err error) {
		if err != nil {
			slog.Warn("Policy file watcher exited, will restart",
				"path", path, "error", err)
			select {
			case diedCh <- struct{}{}:
			default:
			}
			return
		}
		p, loadErr := LoadPolicyFromFile(path)
		if loadErr != nil {
			callback(authz.Policy{}, fmt.Errorf("failed to reload policy file %s: %w", path, loadErr))
			return
		}
		callback(p, nil)
	}

	// Start the initial watch synchronously so it's active before we return.
	if err := fp.Watch(watchFunc); err != nil {
		return authz.Policy{}, nil, &InitializeWatchError{Err: err}
	}

	// Restart loop: when the watcher dies, restart it after a backoff.
	// Koanf sets isWatching=false when its goroutine exits, so fp.Watch
	// can be called again on the same provider.
	go func() {
		for {
			select {
			case <-stopCh:
				return
			case <-diedCh:
			}

			select {
			case <-stopCh:
				return
			case <-time.After(time.Second):
			}

			if err := fp.Watch(watchFunc); err != nil {
				slog.Warn("Failed to restart policy file watcher, will retry",
					"path", path, "error", err)
				// Signal ourselves to retry next iteration.
				select {
				case diedCh <- struct{}{}:
				default:
				}
				continue
			}
			slog.Info("Policy file watcher restarted", "path", path)

			// Reload — we may have missed updates while dead.
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

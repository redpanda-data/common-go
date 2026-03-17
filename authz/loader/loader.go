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
func WatchPolicyFile(path string, callback PolicyCallback) (authz.Policy, PolicyUnwatch, error) {
	k := koanf.New(".")
	fp := file.Provider(path)
	if err := k.Load(fp, yaml.Parser()); err != nil {
		return authz.Policy{}, nil, fmt.Errorf("failed to load policy file %s: %w", path, err)
	}
	var policy authz.Policy
	if err := k.Unmarshal("", &policy); err != nil {
		return authz.Policy{}, nil, fmt.Errorf("failed to unmarshal file %s: %w", path, err)
	}
	// startWatch starts a koanf file watcher. On file change it reloads the
	// policy and calls callback. On watcher error (e.g. transient symlink
	// removal during a Kubernetes ConfigMap update, or neovim atomic save
	// on Linux) it restarts the watch after a short delay.
	var startWatch func()
	startWatch = func() {
		fp := file.Provider(path)
		if err := fp.Watch(func(_ any, watchErr error) {
			if watchErr != nil {
				slog.Warn("Policy file watcher exited, will restart",
					"path", path, "error", watchErr)
				time.Sleep(time.Second)
				startWatch()
				// Reload immediately -- we may have missed updates while dead.
				if p, err := LoadPolicyFromFile(path); err == nil {
					slog.Info("Policy file watcher restarted",
						"path", path)
					callback(p, nil)
				}
				return
			}
			k := koanf.New(".")
			if err := k.Load(fp, yaml.Parser()); err != nil {
				callback(authz.Policy{}, fmt.Errorf("failed to reload policy file %s: %w", path, err))
				return
			}
			var p authz.Policy
			if err := k.Unmarshal("", &p); err != nil {
				callback(authz.Policy{}, fmt.Errorf("failed to unmarshal policy: %w", err))
				return
			}
			callback(p, nil)
		}); err != nil {
			slog.Error("Failed to start policy file watcher",
				"path", path, "error", err)
		}
	}
	startWatch()
	return policy, func() error { return nil }, nil
}

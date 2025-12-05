// Copyright 2025 Redpanda Data, Inc.
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

package loader

import (
	"fmt"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"

	"github.com/redpanda-data/common-go/authz"
)

// LoadPolicyFromFile loads a Policy from a YAML file using koanf.
func LoadPolicyFromFile(path string) (*authz.Policy, error) {
	k := koanf.New(".")
	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("failed to load policy file %s: %w", path, err)
	}
	var policy authz.Policy
	if err := k.Unmarshal("", &policy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal policy: %w", err)
	}

	return &policy, nil
}

// LoadPolicyFromBytes loads a Policy from YAML bytes using koanf.
func LoadPolicyFromBytes(data []byte) (*authz.Policy, error) {
	k := koanf.New(".")
	if err := k.Load(rawbytes.Provider(data), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("failed to load policy data: %w", err)
	}
	var policy authz.Policy
	if err := k.Unmarshal("", &policy); err != nil {
		return nil, fmt.Errorf("failed to unmarshal policy: %w", err)
	}
	return &policy, nil
}

// PolicyCallback is called when a policy is loaded or reloaded.
// If an error occurs during loading, policy will be empty and err will be set.
type PolicyCallback func(policy authz.Policy, err error)

// PolicyUnwatch stops watching the policy file for changes.
type PolicyUnwatch func() error

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
	// Watch for changes using the file provider's Watch method
	// The watchFunc will be called whenever the file changes
	watchFunc := func(_ any, err error) {
		if err != nil {
			callback(authz.Policy{}, fmt.Errorf("watcher error: %w", err))
			return
		}
		// Reload the policy
		k := koanf.New(".")
		if err := k.Load(fp, yaml.Parser()); err != nil {
			callback(authz.Policy{}, fmt.Errorf("failed to reload policy file %s: %w", path, err))
			return
		}
		var policy authz.Policy
		if err := k.Unmarshal("", &policy); err != nil {
			callback(authz.Policy{}, fmt.Errorf("failed to unmarshal policy: %w", err))
			return
		}
		callback(policy, nil)
	}
	done := make(chan struct{})
	// TODO: Make it impossible to drop events between the initial load and updates
	// right now we can't "wait" for the Watch to start so there is a small window
	// when updates could be dropped.
	go func() {
		defer close(done)
		err := fp.Watch(watchFunc)
		if err != nil {
			callback(authz.Policy{}, fmt.Errorf("failed to watch policy for changes: %w", err))
		}
	}()
	return policy, func() error {
		err := fp.Unwatch()
		<-done
		return err
	}, nil
}

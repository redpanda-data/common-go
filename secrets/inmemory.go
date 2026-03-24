// Copyright 2026 Redpanda Data, Inc.
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

package secrets

import "context"

// NewInMemoryStore creates a SecretAPI backed by a static map.
// Useful for testing and local development.
func NewInMemoryStore(secrets map[string]string) SecretAPI {
	m := make(map[string]string, len(secrets))
	for k, v := range secrets {
		m[k] = v
	}
	return &inMemoryStore{secrets: m}
}

type inMemoryStore struct {
	secrets map[string]string
}

func (s *inMemoryStore) GetSecretValue(_ context.Context, key string) (string, bool) {
	v, ok := s.secrets[key]
	return v, ok
}

func (s *inMemoryStore) CheckSecretExists(_ context.Context, key string) bool {
	_, ok := s.secrets[key]
	return ok
}

func (*inMemoryStore) GetSecretLabels(context.Context, string) (map[string]string, bool) {
	return nil, false
}

func (s *inMemoryStore) CreateSecret(_ context.Context, key, value string, _ map[string]string) error {
	s.secrets[key] = value
	return nil
}

func (s *inMemoryStore) UpdateSecret(_ context.Context, key, value string, _ map[string]string) error {
	s.secrets[key] = value
	return nil
}

func (s *inMemoryStore) DeleteSecret(_ context.Context, key string) error {
	delete(s.secrets, key)
	return nil
}

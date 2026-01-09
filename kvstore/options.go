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

package kvstore

import (
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
)

// clientConfig holds configuration for creating a Client.
type clientConfig struct {
	brokers           []string
	replicationFactor int16
	kafkaOptions      []kgo.Opt
	storage           Storage
	logger            *slog.Logger
	onSet             func(key, value []byte)
	onDelete          func(key []byte)
}

// ClientOption configures a Client.
type ClientOption func(*clientConfig)

// WithBrokers sets the Kafka broker addresses.
func WithBrokers(brokers ...string) ClientOption {
	return func(c *clientConfig) {
		c.brokers = brokers
	}
}

// WithReplicationFactor sets the topic replication factor.
func WithReplicationFactor(rf int16) ClientOption {
	return func(c *clientConfig) {
		c.replicationFactor = rf
	}
}

// WithKafkaOptions passes additional options to the franz-go client.
func WithKafkaOptions(opts ...kgo.Opt) ClientOption {
	return func(c *clientConfig) {
		c.kafkaOptions = append(c.kafkaOptions, opts...)
	}
}

// WithLogger sets the logger for client operations.
// If not provided, defaults to slog.Default().
func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *clientConfig) {
		c.logger = logger
	}
}

// WithOnSetHook registers a callback that fires after each successful Set operation
// in the consumer. This includes both bootstrap replay and live updates.
// The callback receives the key and value bytes.
func WithOnSetHook(fn func(key, value []byte)) ClientOption {
	return func(c *clientConfig) {
		c.onSet = fn
	}
}

// WithOnDeleteHook registers a callback that fires after each successful Delete operation
// in the consumer. This includes both bootstrap replay and live updates.
// The callback receives the key bytes.
func WithOnDeleteHook(fn func(key []byte)) ClientOption {
	return func(c *clientConfig) {
		c.onDelete = fn
	}
}

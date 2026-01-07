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

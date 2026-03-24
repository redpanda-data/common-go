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
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/sr"
	"google.golang.org/protobuf/proto"
)

// ProtoSerde serializes values as protobuf, optionally with Schema Registry wire format.
type ProtoSerde[T proto.Message] struct {
	new   func() T
	serde *sr.Serde
}

// ProtoOption configures a ProtoSerde.
type ProtoOption func(*protoConfig)

type protoConfig struct {
	srConfig *srSerdeConfig
	err      error
}

type srSerdeConfig struct {
	schemaID     int
	messageIndex int
}

// Proto returns a protobuf serde for type T.
// The factory function must return a new instance of the proto message.
//
// Example basic usage:
//
//	serde, err := kvstore.Proto(func() *pb.MyMessage { return &pb.MyMessage{} })
//
// Example with Schema Registry:
//
//	serde, err := kvstore.Proto(
//	    func() *pb.MyMessage { return &pb.MyMessage{} },
//	    kvstore.WithSchemaRegistry(srClient, "topic-value", protoSchema),
//	)
func Proto[T proto.Message](factory func() T, opts ...ProtoOption) (Serde[T], error) {
	cfg := &protoConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.err != nil {
		return nil, cfg.err
	}

	var serde *sr.Serde
	if cfg.srConfig != nil {
		// Create sr.Serde with the factory for proper type instantiation
		serde = sr.NewSerde()
		serde.Register(
			cfg.srConfig.schemaID,
			factory(), // Pass concrete type instance
			sr.GenerateFn(func() any {
				return factory() // Tell sr.Serde how to create new instances
			}),
			sr.EncodeFn(func(v any) ([]byte, error) {
				return proto.Marshal(v.(proto.Message))
			}),
			sr.DecodeFn(func(b []byte, v any) error {
				return proto.Unmarshal(b, v.(proto.Message))
			}),
			sr.Index(cfg.srConfig.messageIndex),
		)
	}

	return &ProtoSerde[T]{
		new:   factory,
		serde: serde,
	}, nil
}

// Serialize marshals the protobuf message.
// If Schema Registry is configured, wraps with Confluent wire format.
func (s *ProtoSerde[T]) Serialize(v T) ([]byte, error) {
	// If Schema Registry configured, use serde which handles wire format
	if s.serde != nil {
		return s.serde.Encode(v)
	}

	// Plain protobuf without Schema Registry
	return proto.Marshal(v)
}

// Deserialize unmarshals protobuf bytes to the message.
// If Schema Registry is configured, decodes Confluent wire format first.
func (s *ProtoSerde[T]) Deserialize(b []byte) (T, error) {
	// If Schema Registry configured, use serde which handles wire format
	if s.serde != nil {
		v, err := s.serde.DecodeNew(b)
		if err != nil {
			var zero T
			return zero, err
		}
		result, ok := v.(T)
		if !ok {
			var zero T
			return zero, fmt.Errorf("decoded value is not of expected type %T", zero)
		}
		return result, nil
	}

	// Plain protobuf without Schema Registry
	v := s.new()
	err := proto.Unmarshal(b, v)
	return v, err
}

// WithSchemaRegistry configures Schema Registry support for ProtoSerde.
// This wraps protobuf payloads with Confluent wire format for schema governance.
// Schema is registered immediately at initialization time.
//
// Parameters:
//   - srClient: Schema Registry client
//   - subject: Subject name (typically "{topic}-value")
//   - schemaContent: Proto file content as string
//   - opts: Optional references for proto imports
//
// Example:
//
//	serde, err := kvstore.Proto(
//	    func() *pb.MyMessage { return &pb.MyMessage{} },
//	    kvstore.WithSchemaRegistry(srClient, "topic-value", protoSchema),
//	)
func WithSchemaRegistry(
	srClient *sr.Client,
	subject string,
	schemaContent string,
	opts ...SchemaRegistryOption,
) ProtoOption {
	cfg := &srConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// Resolve message index by name if specified.
	messageIndex := 0
	if cfg.messageName != "" {
		idx, err := findMessageIndex(schemaContent, cfg.messageName)
		if err != nil {
			return func(c *protoConfig) {
				c.err = fmt.Errorf("resolve message index for %q: %w", cfg.messageName, err)
			}
		}
		messageIndex = idx
	}

	// Register schema immediately.
	schema := sr.Schema{
		Schema:     schemaContent,
		Type:       sr.TypeProtobuf,
		References: cfg.refs,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := srClient.CreateSchema(ctx, subject, schema)
	if err != nil {
		return func(c *protoConfig) {
			c.err = fmt.Errorf("schema registration for subject %s: %w", subject, err)
		}
	}

	// Store the schema ID and message index for sr.Serde creation in Proto().
	return func(c *protoConfig) {
		c.srConfig = &srSerdeConfig{
			schemaID:     result.ID,
			messageIndex: messageIndex,
		}
	}
}

// SchemaRegistryOption configures Schema Registry behavior.
type SchemaRegistryOption func(*srConfig)

type srConfig struct {
	refs        []sr.SchemaReference
	messageName string
}

// WithMessageName specifies which message in the proto file is the serialized payload.
// The message index is resolved by scanning the proto content for top-level message
// declarations. This avoids hardcoding indices that break when messages are reordered.
//
// If not specified, the first message (index 0) is used.
//
// Example:
//
//	kvstore.WithSchemaRegistry(srClient, "topic-value", protoSchema,
//	    kvstore.WithMessageName("LLMProvider"),
//	)
func WithMessageName(name string) SchemaRegistryOption {
	return func(c *srConfig) {
		c.messageName = name
	}
}

// findMessageIndex scans proto content for top-level "message <Name>" declarations
// and returns the zero-based index of the named message.
func findMessageIndex(protoContent, messageName string) (int, error) {
	// Match top-level message declarations. This regex handles the common case;
	// nested messages are not counted (they don't get their own wire-format index).
	re := regexp.MustCompile(`(?m)^message\s+(\w+)\s*\{`)
	matches := re.FindAllStringSubmatch(protoContent, -1)
	for i, m := range matches {
		if m[1] == messageName {
			return i, nil
		}
	}
	var found []string
	for _, m := range matches {
		found = append(found, m[1])
	}
	return 0, fmt.Errorf("message %q not found in proto; found: [%s]", messageName, strings.Join(found, ", "))
}

// WithSchemaReferences specifies proto import dependencies.
//
// Example:
//
//	WithSchemaReferences([]sr.SchemaReference{
//	    {Name: "common.proto", Subject: "topic-common", Version: 1},
//	})
func WithSchemaReferences(refs []sr.SchemaReference) SchemaRegistryOption {
	return func(c *srConfig) {
		c.refs = refs
	}
}

// NewSchemaRegistrySerde creates a protobuf serde with Schema Registry support.
//
// Deprecated: Use Proto with WithSchemaRegistry instead.
func NewSchemaRegistrySerde[T proto.Message](
	srClient *sr.Client,
	subject string,
	schemaContent string,
	factory func() T,
	opts ...SchemaRegistryOption,
) (Serde[T], error) {
	return Proto(factory, WithSchemaRegistry(srClient, subject, schemaContent, opts...))
}

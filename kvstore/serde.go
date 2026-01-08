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
	"encoding/json"

	"google.golang.org/protobuf/proto"
)

// JSONSerde serializes values as JSON.
type JSONSerde[T any] struct{}

// JSON returns a JSON serde for type T.
func JSON[T any]() Serde[T] {
	return &JSONSerde[T]{}
}

// Serialize marshals the value to JSON.
func (*JSONSerde[T]) Serialize(v T) ([]byte, error) {
	return json.Marshal(v)
}

// Deserialize unmarshals JSON bytes to the value.
func (*JSONSerde[T]) Deserialize(b []byte) (T, error) {
	var v T
	err := json.Unmarshal(b, &v)
	return v, err
}

// ProtoSerde serializes values as protobuf.
type ProtoSerde[T proto.Message] struct {
	new func() T
}

// Proto returns a protobuf serde for type T.
// The factory function must return a new instance of the proto message.
func Proto[T proto.Message](factory func() T) Serde[T] {
	return &ProtoSerde[T]{new: factory}
}

// Serialize marshals the protobuf message.
func (*ProtoSerde[T]) Serialize(v T) ([]byte, error) {
	return proto.Marshal(v)
}

// Deserialize unmarshals protobuf bytes to the message.
func (s *ProtoSerde[T]) Deserialize(b []byte) (T, error) {
	v := s.new()
	err := proto.Unmarshal(b, v)
	return v, err
}

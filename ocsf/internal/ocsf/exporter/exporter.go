// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Package exporter marshals generated OCSF proto messages to schema-valid
// OCSF JSON, fixing three things that stock protojson gets wrong:
//
//  1. ENUMS: protojson emits the enum value name (e.g. "SEVERITY_ID_CRITICAL");
//     OCSF requires the integer (e.g. 5).
//
//  2. 64-BIT INTEGERS: protojson emits int64/uint64 etc. as a quoted string
//     (e.g. "1685403212834"); OCSF timestamp_t and type_uid are JSON numbers.
//
//  3. KEY CASING: protojson defaults to camelCase JSON names (e.g. "classUid");
//     OCSF keys are snake_case, matching the proto field names (e.g. "class_uid").
package exporter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
)

// wellKnownStructural is the set of google.protobuf well-known wrapper and
// structural message full names that we delegate to protojson for natural
// JSON rendering instead of recursing into their internal fields.
var wellKnownStructural = map[protoreflect.FullName]bool{
	"google.protobuf.Value":     true,
	"google.protobuf.Struct":    true,
	"google.protobuf.ListValue": true,
	// Wrappers: delegate to protojson too.
	"google.protobuf.DoubleValue": true,
	"google.protobuf.FloatValue":  true,
	"google.protobuf.Int64Value":  true,
	"google.protobuf.UInt64Value": true,
	"google.protobuf.Int32Value":  true,
	"google.protobuf.UInt32Value": true,
	"google.protobuf.BoolValue":   true,
	"google.protobuf.StringValue": true,
	"google.protobuf.BytesValue":  true,
}

// ToOCSFJSON marshals a proto.Message to OCSF-compliant JSON.
//
// Contract:
//   - Keys are the proto field names (snake_case).
//   - Enum fields are emitted as their integer values, not name strings.
//   - int64/uint64/sint64/sfixed64/fixed64 are emitted as unquoted JSON numbers.
//   - google.protobuf.Value/Struct/ListValue are emitted as their natural JSON.
//   - Unset fields are omitted: proto3 implicit-presence scalars at zero value,
//     explicit optional fields that are nil, empty repeated/map, nil messages.
//   - repeated -> JSON array; nested message -> JSON object (recursively).
//   - Fields are emitted in ascending field-number order (deterministic).
func ToOCSFJSON(m proto.Message) ([]byte, error) {
	var buf bytes.Buffer
	if err := marshalMessage(&buf, m.ProtoReflect()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// marshalMessage writes a single proto message as a JSON object to buf.
func marshalMessage(buf *bytes.Buffer, msg protoreflect.Message) error {
	// Delegate well-known structural types to protojson for natural rendering.
	if wellKnownStructural[msg.Descriptor().FullName()] {
		return marshalWellKnown(buf, msg)
	}

	// Collect set fields sorted by field number.
	fields := collectSetFields(msg)

	buf.WriteByte('{')
	first := true
	for _, fd := range fields {
		val := msg.Get(fd)

		var valBuf bytes.Buffer
		if err := marshalValue(&valBuf, fd, val); err != nil {
			return fmt.Errorf("field %q: %w", fd.Name(), err)
		}

		if !first {
			buf.WriteByte(',')
		}
		first = false

		// Key: proto field name (snake_case).
		key, err := json.Marshal(string(fd.Name()))
		if err != nil {
			return err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(valBuf.Bytes())
	}
	buf.WriteByte('}')
	return nil
}

// collectSetFields returns the field descriptors for all set (non-zero) fields
// of msg, sorted by ascending field number.
//
// For proto3 implicit-presence scalars, "set" means the value is not the zero
// value. For fields with explicit presence (optional, oneof, message), "set"
// means HasField returns true.
//
// Note: protoreflect.Message.Range visits set fields in undefined order. The
// explicit sort.Slice by field number below is what guarantees deterministic
// output.
func collectSetFields(msg protoreflect.Message) []protoreflect.FieldDescriptor {
	var fds []protoreflect.FieldDescriptor
	msg.Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
		fds = append(fds, fd)
		return true
	})
	sort.Slice(fds, func(i, j int) bool {
		return fds[i].Number() < fds[j].Number()
	})
	return fds
}

// marshalValue writes a single proto field value as JSON to buf.
func marshalValue(buf *bytes.Buffer, fd protoreflect.FieldDescriptor, val protoreflect.Value) error {
	// Repeated field -> JSON array.
	if fd.IsList() {
		return marshalList(buf, fd, val.List())
	}

	// Map field -> JSON object.
	if fd.IsMap() {
		return marshalMap(buf, fd, val.Map())
	}

	return marshalSingular(buf, fd, val)
}

// marshalList writes a repeated field as a JSON array.
func marshalList(buf *bytes.Buffer, fd protoreflect.FieldDescriptor, list protoreflect.List) error {
	buf.WriteByte('[')
	for i := range list.Len() {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := marshalSingular(buf, fd, list.Get(i)); err != nil {
			return err
		}
	}
	buf.WriteByte(']')
	return nil
}

// marshalMap writes a map field as a JSON object.
func marshalMap(buf *bytes.Buffer, fd protoreflect.FieldDescriptor, m protoreflect.Map) error {
	valFD := fd.MapValue()

	// Collect keys for deterministic ordering.
	keys := make([]protoreflect.MapKey, 0, m.Len())
	m.Range(func(k protoreflect.MapKey, _ protoreflect.Value) bool {
		keys = append(keys, k)
		return true
	})
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})

	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		// Map keys are always strings in JSON.
		keyBytes, err := json.Marshal(k.String())
		if err != nil {
			return err
		}
		buf.Write(keyBytes)
		buf.WriteByte(':')

		var valBuf bytes.Buffer
		if err := marshalSingular(&valBuf, valFD, m.Get(k)); err != nil {
			return err
		}
		buf.Write(valBuf.Bytes())
	}
	buf.WriteByte('}')
	return nil
}

// marshalSingular writes a single (non-repeated, non-map) proto value as JSON.
func marshalSingular(buf *bytes.Buffer, fd protoreflect.FieldDescriptor, val protoreflect.Value) error {
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return marshalMessage(buf, val.Message())

	case protoreflect.EnumKind:
		// OCSF requires the integer, not the enum name string.
		fmt.Fprintf(buf, "%d", val.Enum())
		return nil

	case protoreflect.BoolKind:
		if val.Bool() {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
		return nil

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		// OCSF timestamps and type_uid are JSON numbers, not quoted strings.
		fmt.Fprintf(buf, "%d", val.Int())
		return nil

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		fmt.Fprintf(buf, "%d", val.Uint())
		return nil

	case protoreflect.FloatKind:
		b, err := json.Marshal(float32(val.Float()))
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil

	case protoreflect.DoubleKind:
		b, err := json.Marshal(val.Float())
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil

	case protoreflect.StringKind:
		b, err := json.Marshal(val.String())
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil

	case protoreflect.BytesKind:
		// Proto JSON convention: bytes -> base64 standard encoding.
		enc := base64.StdEncoding.EncodeToString(val.Bytes())
		b, err := json.Marshal(enc)
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil

	default:
		return fmt.Errorf("unsupported proto kind %v for field %q", fd.Kind(), fd.Name())
	}
}

// marshalWellKnown delegates well-known structural message serialisation to the
// standard protojson library, which handles the natural JSON forms of
// google.protobuf.Value, Struct, ListValue, and wrapper types correctly.
//
// We then re-emit the protojson output verbatim; these types don't have
// snake_case / int-enum / unquoted-int64 concerns.
func marshalWellKnown(buf *bytes.Buffer, msg protoreflect.Message) error {
	// Extract the concrete proto.Message to pass to the structpb marshaler.
	// The only well-known types we care about in OCSF are Value/Struct/ListValue.
	// Use a type switch on the underlying Go type.
	concrete := msg.Interface()
	switch v := concrete.(type) {
	case *structpb.Value:
		b, err := v.MarshalJSON()
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	case *structpb.Struct:
		b, err := v.MarshalJSON()
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	case *structpb.ListValue:
		b, err := v.MarshalJSON()
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	default:
		// Fallback for other well-known types (wrappers etc.): use protojson.
		// These don't appear in generated OCSF protos but we handle them
		// defensively.
		return marshalWellKnownFallback(buf, msg)
	}
}

// marshalWellKnownFallback uses protojson to serialise any remaining well-known
// types not handled by the type switch above.
func marshalWellKnownFallback(buf *bytes.Buffer, msg protoreflect.Message) error {
	// protojson doesn't export a low-level per-message marshaler easily, so we
	// use the standard proto.Marshal -> json.RawMessage route by delegating
	// through the concrete message interface.
	//
	// This path is only hit for wrapper types (DoubleValue, StringValue, etc.)
	// which are rarely used in OCSF protos.  We use encoding/json to re-encode
	// the protojson output to avoid any escaping surprises.
	type jsonMarshaler interface {
		MarshalJSON() ([]byte, error)
	}
	if jm, ok := msg.Interface().(jsonMarshaler); ok {
		b, err := jm.MarshalJSON()
		if err != nil {
			return err
		}
		buf.Write(b)
		return nil
	}
	return fmt.Errorf("well-known type %q does not implement MarshalJSON", msg.Descriptor().FullName())
}

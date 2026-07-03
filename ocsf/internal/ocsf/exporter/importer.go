// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package exporter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"math"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// unmappedFieldName is the OCSF base_event attribute that carries data the
// producer could not map to schema attributes. FromOCSFJSON routes unknown
// JSON keys into it, mirroring OCSF's own forward-compatibility model.
const unmappedFieldName = "unmapped"

// FromOCSFJSON unmarshals OCSF JSON produced by ToOCSFJSON (or any
// OCSF-conformant producer) into m, the exact inverse of ToOCSFJSON:
//
//   - Keys are the proto field names (snake_case).
//   - Enum fields accept JSON numbers (the OCSF integer convention). Unknown
//     numeric values are stored as-is: proto3 enums are open, so events from a
//     newer OCSF revision round-trip without loss.
//   - 64-bit integers accept unquoted JSON numbers (the OCSF convention) and,
//     for interoperability with protojson producers, quoted decimal strings.
//   - google.protobuf.Value/Struct/ListValue fields take their natural JSON.
//     For Value fields, JSON null decodes to a set NullValue (protojson
//     semantics), preserving the present-with-null vs absent distinction.
//   - JSON null on any other field leaves it unset. Absent keys leave fields
//     at their proto3 defaults, so ToOCSFJSON(FromOCSFJSON(x)) == x for
//     conformant x.
//   - Unknown JSON keys are collected into the message's "unmapped" field
//     (json_t in OCSF base_event) when the message declares one; if it does
//     not, unknown keys are an error naming them.
//
// m is reset before decoding.
func FromOCSFJSON(data []byte, m proto.Message) error {
	proto.Reset(m)
	return unmarshalMessage(data, m.ProtoReflect())
}

// unmarshalMessage decodes a JSON object into msg.
func unmarshalMessage(data []byte, msg protoreflect.Message) error {
	// Well-known structural types take their natural JSON form (including
	// null, which is a valid google.protobuf.Value).
	if wellKnownStructural[msg.Descriptor().FullName()] {
		return protojson.Unmarshal(data, msg.Interface())
	}

	// encoding/json decodes null into a map as a silent no-op; reject it
	// explicitly so `null` never masquerades as an empty event.
	if isJSONNull(data) {
		return fmt.Errorf("%s: expected JSON object, got null", msg.Descriptor().FullName())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("%s: decode JSON object: %w", msg.Descriptor().FullName(), err)
	}

	fields := msg.Descriptor().Fields()
	var unknown map[string]json.RawMessage

	for key, val := range raw {
		fd := fields.ByName(protoreflect.Name(key))
		if fd == nil {
			if isJSONNull(val) {
				continue
			}
			if unknown == nil {
				unknown = make(map[string]json.RawMessage)
			}
			unknown[key] = val
			continue
		}
		// JSON null leaves a field unset — except for google.protobuf.Value,
		// where null IS a value (NullValue) and dropping it would break the
		// export/import symmetry: ToOCSFJSON emits a set null Value as
		// `"key":null`, so that byte sequence must decode back to a set field.
		if isJSONNull(val) && !isValueField(fd) {
			continue
		}
		if err := unmarshalField(msg, fd, val); err != nil {
			return fmt.Errorf("field %q: %w", key, err)
		}
	}

	if len(unknown) > 0 {
		if err := storeUnmapped(msg, fields, unknown); err != nil {
			return err
		}
	}
	return nil
}

// storeUnmapped routes unknown JSON keys into the message's unmapped field.
func storeUnmapped(msg protoreflect.Message, fields protoreflect.FieldDescriptors, unknown map[string]json.RawMessage) error {
	fd := fields.ByName(unmappedFieldName)
	if fd == nil || fd.Kind() != protoreflect.MessageKind ||
		fd.Message().FullName() != "google.protobuf.Value" || fd.IsList() || fd.IsMap() {
		keys := make([]string, 0, len(unknown))
		for k := range unknown {
			keys = append(keys, k)
		}
		return fmt.Errorf("%s: unknown JSON keys %v and no %q google.protobuf.Value field to store them",
			msg.Descriptor().FullName(), keys, unmappedFieldName)
	}

	// If the input itself carried an "unmapped" object, merge the unknown keys
	// into it rather than overwriting.
	obj := make(map[string]json.RawMessage, len(unknown)+4)
	if msg.Has(fd) {
		existing, err := protojson.Marshal(msg.Get(fd).Message().Interface())
		if err != nil {
			return fmt.Errorf("re-marshal existing %s: %w", unmappedFieldName, err)
		}
		// Non-object unmapped (string, list, ...) cannot absorb extra keys.
		if err := json.Unmarshal(existing, &obj); err != nil {
			return fmt.Errorf("%s: %q is set to a non-object value; cannot merge unknown JSON keys into it",
				msg.Descriptor().FullName(), unmappedFieldName)
		}
		// json.Unmarshal of `null` nils the map without error: an explicit
		// null unmapped carries no data, so treat it as empty and let the
		// unknown keys replace it.
		if obj == nil {
			obj = make(map[string]json.RawMessage, len(unknown))
		}
	}
	maps.Copy(obj, unknown)

	merged, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	val := msg.NewField(fd)
	if err := protojson.Unmarshal(merged, val.Message().Interface()); err != nil {
		return fmt.Errorf("store unknown keys in %q: %w", unmappedFieldName, err)
	}
	msg.Set(fd, val)
	return nil
}

// unmarshalField decodes a single JSON value into fd on msg.
func unmarshalField(msg protoreflect.Message, fd protoreflect.FieldDescriptor, data json.RawMessage) error {
	switch {
	case fd.IsList():
		var items []json.RawMessage
		if err := json.Unmarshal(data, &items); err != nil {
			return fmt.Errorf("expected JSON array: %w", err)
		}
		list := msg.Mutable(fd).List()
		for i, item := range items {
			v, err := unmarshalSingular(fd, item, list.NewElement)
			if err != nil {
				return fmt.Errorf("index %d: %w", i, err)
			}
			list.Append(v)
		}
		return nil

	case fd.IsMap():
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(data, &obj); err != nil {
			return fmt.Errorf("expected JSON object: %w", err)
		}
		mp := msg.Mutable(fd).Map()
		valFD := fd.MapValue()
		for k, item := range obj {
			key, err := mapKey(fd.MapKey(), k)
			if err != nil {
				return fmt.Errorf("key %q: %w", k, err)
			}
			v, err := unmarshalSingular(valFD, item, func() protoreflect.Value { return mp.NewValue() })
			if err != nil {
				return fmt.Errorf("key %q: %w", k, err)
			}
			mp.Set(key, v)
		}
		return nil

	case fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind:
		val := msg.NewField(fd)
		if err := unmarshalMessage(data, val.Message()); err != nil {
			return err
		}
		msg.Set(fd, val)
		return nil

	default:
		v, err := unmarshalScalar(fd, data)
		if err != nil {
			return err
		}
		msg.Set(fd, v)
		return nil
	}
}

// unmarshalSingular decodes one array element or map value.
func unmarshalSingular(fd protoreflect.FieldDescriptor, data json.RawMessage, newMessage func() protoreflect.Value) (protoreflect.Value, error) {
	if fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind {
		val := newMessage()
		if err := unmarshalMessage(data, val.Message()); err != nil {
			return protoreflect.Value{}, err
		}
		return val, nil
	}
	return unmarshalScalar(fd, data)
}

// unmarshalScalar decodes a JSON scalar into a proto scalar value, the inverse
// of marshalSingular's non-message cases. Numeric kinds are dispatched to
// unmarshalNumericScalar.
func unmarshalScalar(fd protoreflect.FieldDescriptor, data json.RawMessage) (protoreflect.Value, error) {
	// encoding/json decodes null into string/bool targets as a silent no-op;
	// reject it explicitly so array elements and map values never silently
	// become zero values (OCSF arrays carry no nulls).
	if isJSONNull(data) {
		return protoreflect.Value{}, fmt.Errorf("unexpected JSON null for %v", fd.Kind())
	}

	switch fd.Kind() {
	case protoreflect.BoolKind:
		var b bool
		if err := json.Unmarshal(data, &b); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfBool(b), nil

	case protoreflect.StringKind:
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfString(s), nil

	case protoreflect.BytesKind:
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return protoreflect.Value{}, err
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("base64: %w", err)
		}
		return protoreflect.ValueOfBytes(b), nil

	default:
		return unmarshalNumericScalar(fd, data)
	}
}

// unmarshalNumericScalar decodes the enum, integer, and floating-point kinds.
func unmarshalNumericScalar(fd protoreflect.FieldDescriptor, data json.RawMessage) (protoreflect.Value, error) {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		n, err := decodeInt32(data)
		if err != nil {
			return protoreflect.Value{}, fmt.Errorf("enum: %w", err)
		}
		return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)), nil

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := decodeInt32(data)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt32(n), nil

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := decodeInt(data)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfInt64(n), nil

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := decodeUint32(data)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint32(n), nil

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := decodeUint(data)
		if err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfUint64(n), nil

	case protoreflect.FloatKind:
		var f float64
		if err := json.Unmarshal(data, &f); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat32(float32(f)), nil

	case protoreflect.DoubleKind:
		var f float64
		if err := json.Unmarshal(data, &f); err != nil {
			return protoreflect.Value{}, err
		}
		return protoreflect.ValueOfFloat64(f), nil

	default:
		return protoreflect.Value{}, fmt.Errorf("unsupported proto kind %v", fd.Kind())
	}
}

// mapKey converts a JSON object key string into a proto map key.
func mapKey(fd protoreflect.FieldDescriptor, key string) (protoreflect.MapKey, error) {
	v, err := unmarshalScalar(fd, mustQuote(fd, key))
	if err != nil {
		return protoreflect.MapKey{}, err
	}
	return v.MapKey(), nil
}

// mustQuote re-encodes a map key string as the JSON literal the scalar decoder
// expects: quoted for string keys, bare for numeric/bool keys.
func mustQuote(fd protoreflect.FieldDescriptor, key string) json.RawMessage {
	if fd.Kind() == protoreflect.StringKind {
		quoted, _ := json.Marshal(key)
		return quoted
	}
	return json.RawMessage(key)
}

// decodeNumber extracts the decimal literal from data: a bare JSON number, or
// (for protojson interoperability, which quotes 64-bit integers) a JSON string
// containing one.
func decodeNumber(data json.RawMessage) (json.Number, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", fmt.Errorf("expected JSON number: %w", err)
		}
		return json.Number(s), nil
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	if err := dec.Decode(&n); err != nil {
		return "", fmt.Errorf("expected JSON number: %w", err)
	}
	return n, nil
}

// decodeInt parses a JSON number (or quoted decimal string) as int64 without
// float64 precision loss.
func decodeInt(data json.RawMessage) (int64, error) {
	n, err := decodeNumber(data)
	if err != nil {
		return 0, err
	}
	return n.Int64()
}

// decodeInt32 parses a JSON number as int32, rejecting overflow.
func decodeInt32(data json.RawMessage) (int32, error) {
	n, err := decodeInt(data)
	if err != nil {
		return 0, err
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("value %d overflows int32", n)
	}
	return int32(n), nil
}

// decodeUint32 parses a JSON number as uint32, rejecting overflow.
func decodeUint32(data json.RawMessage) (uint32, error) {
	n, err := decodeUint(data)
	if err != nil {
		return 0, err
	}
	if n > math.MaxUint32 {
		return 0, fmt.Errorf("value %d overflows uint32", n)
	}
	return uint32(n), nil
}

// decodeUint parses a JSON number (or quoted decimal string) as uint64.
func decodeUint(data json.RawMessage) (uint64, error) {
	n, err := decodeNumber(data)
	if err != nil {
		return 0, err
	}
	var u uint64
	if _, err := fmt.Sscanf(n.String(), "%d", &u); err != nil {
		return 0, fmt.Errorf("parse %q as uint64: %w", n.String(), err)
	}
	return u, nil
}

// isJSONNull reports whether data is the JSON literal null.
func isJSONNull(data json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(data), []byte("null"))
}

// isValueField reports whether fd is a singular google.protobuf.Value field.
func isValueField(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.MessageKind && !fd.IsList() && !fd.IsMap() &&
		fd.Message().FullName() == "google.protobuf.Value"
}

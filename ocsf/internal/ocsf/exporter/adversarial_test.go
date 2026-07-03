// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Adversarial tests for the JSON ↔ proto transformation: hostile inputs must
// produce errors, never panics, silent data corruption, or asymmetric
// round-trips.
package exporter_test

import (
	"bytes"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
	samplev1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter/testdata/sample"
)

// TestFromOCSFJSON_MalformedInputs feeds structurally broken JSON and asserts
// an error (never a panic, never silent success).
func TestFromOCSFJSON_MalformedInputs(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"Empty", ``},
		{"Whitespace", `   `},
		{"Garbage", `!!!not json!!!`},
		{"TruncatedObject", `{"time": 123`},
		{"TruncatedString", `{"message_text": "unterminated`},
		{"TopLevelNull", `null`},
		{"TopLevelArray", `[{"time":1}]`},
		{"TopLevelString", `"just a string"`},
		{"TopLevelNumber", `42`},
		{"TopLevelBool", `true`},
		{"TrailingGarbage", `{"time":1}}}}`},
		{"UnbalancedBrackets", `{"actor":{"name":"x"}`},
		{"BareComma", `{"time":1,}`},
		{"SingleQuotes", `{'time':1}`},
		{"ControlCharInKey", "{\"ti\x00me\":1}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var evt samplev1.SampleEvent
			require.NotPanics(t, func() {
				err := exporter.FromOCSFJSON([]byte(tc.input), &evt)
				require.Error(t, err, "malformed input %q must be rejected", tc.input)
			})
		})
	}
}

// TestFromOCSFJSON_TypeConfusion sends the wrong JSON type for every field
// kind and asserts an error naming the field.
func TestFromOCSFJSON_TypeConfusion(t *testing.T) {
	cases := []struct {
		name  string
		input string
		field string
	}{
		{"StringIntoInt64", `{"time":"not-a-number"}`, "time"},
		{"BoolIntoInt64", `{"time":true}`, "time"},
		{"ObjectIntoInt64", `{"time":{"epoch":1}}`, "time"},
		{"ArrayIntoInt64", `{"time":[1]}`, "time"},
		{"NumberIntoString", `{"message_text":42}`, "message_text"},
		{"ObjectIntoString", `{"message_text":{"x":1}}`, "message_text"},
		{"StringIntoBool", `{"is_alert":"yes"}`, "is_alert"},
		{"NumberIntoBool", `{"is_alert":1}`, "is_alert"},
		{"StringIntoEnum", `{"severity_id":"CRITICAL"}`, "severity_id"},
		{"ObjectIntoEnum", `{"severity_id":{"id":5}}`, "severity_id"},
		{"StringIntoDouble", `{"score":"high"}`, "score"},
		{"ScalarIntoMessage", `{"actor":"alice"}`, "actor"},
		{"ArrayIntoMessage", `{"actor":["alice"]}`, "actor"},
		{"NumberIntoMessage", `{"actor":7}`, "actor"},
		{"ObjectIntoRepeated", `{"tags":{"a":"b"}}`, "tags"},
		{"ScalarIntoRepeated", `{"tags":"solo"}`, "tags"},
		{"WrongElementInRepeated", `{"tags":["ok",42]}`, "tags"},
		{"WrongElementInRepeatedEnum", `{"severities":[5,"critical"]}`, "severities"},
		{"ObjectElementInScalarArray", `{"tags":[{"x":1}]}`, "tags"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var evt samplev1.SampleEvent
			require.NotPanics(t, func() {
				err := exporter.FromOCSFJSON([]byte(tc.input), &evt)
				require.Error(t, err)
				require.ErrorContains(t, err, tc.field, "error must name the offending field")
			})
		})
	}
}

// TestFromOCSFJSON_NumericEdgeCases probes the integer decoding boundaries.
func TestFromOCSFJSON_NumericEdgeCases(t *testing.T) {
	t.Run("Int64Bounds", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"time":9223372036854775807}`), &evt))
		require.Equal(t, int64(math.MaxInt64), evt.Time)

		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"time":-9223372036854775808}`), &evt))
		require.Equal(t, int64(math.MinInt64), evt.Time)
	})

	t.Run("Int64Overflow", func(t *testing.T) {
		var evt samplev1.SampleEvent
		err := exporter.FromOCSFJSON([]byte(`{"time":9223372036854775808}`), &evt) // MaxInt64+1
		require.Error(t, err, "int64 overflow must be rejected, not wrapped")
	})

	t.Run("Int32Overflow", func(t *testing.T) {
		var evt samplev1.SampleEvent
		err := exporter.FromOCSFJSON([]byte(`{"count":2147483648}`), &evt) // MaxInt32+1
		require.ErrorContains(t, err, "overflows int32")

		err = exporter.FromOCSFJSON([]byte(`{"count":-2147483649}`), &evt) // MinInt32-1
		require.ErrorContains(t, err, "overflows int32")
	})

	t.Run("Int32Bounds", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"count":2147483647}`), &evt))
		require.Equal(t, int32(math.MaxInt32), evt.Count)
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"count":-2147483648}`), &evt))
		require.Equal(t, int32(math.MinInt32), evt.Count)
	})

	t.Run("EnumOverflow", func(t *testing.T) {
		var evt samplev1.SampleEvent
		err := exporter.FromOCSFJSON([]byte(`{"severity_id":2147483648}`), &evt)
		require.ErrorContains(t, err, "overflows int32")
	})

	t.Run("FloatIntoInt", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"time":1.5}`), &evt),
			"fractional value into int64 must be rejected, not truncated")
	})

	t.Run("ExponentIntoInt", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"time":1e3}`), &evt),
			"exponent notation into int64 must be rejected (json.Number is not an integer literal)")
	})

	t.Run("NegativeEnum", func(t *testing.T) {
		// Proto enums are int32; a negative value is representable and must
		// round-trip verbatim even though OCSF never uses negatives.
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"severity_id":-1}`), &evt))
		require.Equal(t, samplev1.SampleEvent_SeverityId(-1), evt.SeverityId)

		b, err := exporter.ToOCSFJSON(&evt)
		require.NoError(t, err)
		require.Contains(t, string(b), `"severity_id":-1`)
	})

	t.Run("LeadingZeros", func(t *testing.T) {
		var evt samplev1.SampleEvent
		// JSON spec forbids leading zeros; encoding/json rejects them.
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"time":007}`), &evt))
	})
}

// TestFromOCSFJSON_StringEdgeCases verifies hostile string content decodes
// and round-trips byte-exactly.
func TestFromOCSFJSON_StringEdgeCases(t *testing.T) {
	values := []struct {
		name string
		val  string
	}{
		{"Unicode", "ünïcødé 日本語 🦀"},
		{"JSONEscapes", `quote " backslash \ slash / newline` + "\n\ttab"},
		{"ControlChars", "bell\a backspace\b formfeed\f return\r"},
		{"NullByte", "null\x00byte"},
		{"HTMLish", `<script>alert("xss")</script> & entities`},
		{"JSONInjection", `","severity_id":99,"x":"`},
		{"VeryLong", strings.Repeat("A", 1<<20)}, // 1 MiB
		{"Empty", ""},
	}

	for _, v := range values {
		t.Run(v.name, func(t *testing.T) {
			original := &samplev1.SampleEvent{MessageText: v.val, Time: 1}

			b, err := exporter.ToOCSFJSON(original)
			require.NoError(t, err)

			var decoded samplev1.SampleEvent
			require.NoError(t, exporter.FromOCSFJSON(b, &decoded))
			require.Equal(t, v.val, decoded.MessageText, "string content must survive the round-trip byte-exactly")

			// The injection attempt must not have smuggled extra fields in.
			require.Zero(t, decoded.SeverityId)
		})
	}
}

// TestToOCSFJSON_NonFiniteFloats verifies NaN/Inf are rejected with an error
// (JSON has no representation for them), not emitted as invalid JSON.
func TestToOCSFJSON_NonFiniteFloats(t *testing.T) {
	for name, val := range map[string]float64{
		"NaN":    math.NaN(),
		"PosInf": math.Inf(1),
		"NegInf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			evt := &samplev1.SampleEvent{Score: val}
			require.NotPanics(t, func() {
				_, err := exporter.ToOCSFJSON(evt)
				require.Error(t, err, "non-finite float must fail to export")
			})
		})
	}
}

// TestToOCSFJSON_InvalidUTF8 is the regression test for a bug found by
// FuzzToOCSFJSON: a proto string carrying invalid UTF-8 must fail to export.
// encoding/json would otherwise silently replace the invalid bytes with
// U+FFFD, corrupting the value while reporting success.
func TestToOCSFJSON_InvalidUTF8(t *testing.T) {
	cases := []string{
		"\xff",
		"\xc3(",              // truncated 2-byte sequence
		"valid prefix \xf0(", // truncated 4-byte sequence
	}
	for _, s := range cases {
		evt := &samplev1.SampleEvent{MessageText: s}
		_, err := exporter.ToOCSFJSON(evt)
		require.ErrorContains(t, err, "invalid UTF-8",
			"invalid UTF-8 %q must be rejected, not silently replaced", s)

		// Repeated string elements go through the same path.
		evt = &samplev1.SampleEvent{Tags: []string{"ok", s}}
		_, err = exporter.ToOCSFJSON(evt)
		require.ErrorContains(t, err, "invalid UTF-8")
	}
}

// TestFromOCSFJSON_DeepNesting verifies pathological nesting depth in a Value
// field and in message fields errors or succeeds gracefully — no stack
// exhaustion, no panic.
func TestFromOCSFJSON_DeepNesting(t *testing.T) {
	t.Run("DeepValue", func(t *testing.T) {
		const depth = 1000
		payload := `{"metadata":` + strings.Repeat(`{"n":`, depth) + `1` + strings.Repeat(`}`, depth) + `}`

		var evt samplev1.SampleEvent
		require.NotPanics(t, func() {
			err := exporter.FromOCSFJSON([]byte(payload), &evt)
			// protojson enforces its own recursion limit on Value; either
			// outcome is acceptable as long as it is graceful.
			if err == nil {
				_, err = exporter.ToOCSFJSON(&evt)
				require.NoError(t, err, "a successfully decoded deep Value must re-export")
			}
		})
	})

	t.Run("DeepWrongMessage", func(t *testing.T) {
		const depth = 1000
		payload := `{"actor":` + strings.Repeat(`{"name":`, depth) + `"x"` + strings.Repeat(`}`, depth) + `}`

		var evt samplev1.SampleEvent
		require.NotPanics(t, func() {
			require.Error(t, exporter.FromOCSFJSON([]byte(payload), &evt),
				"nesting an object into a string field must error")
		})
	})
}

// TestFromOCSFJSON_DuplicateKeys documents last-wins semantics for duplicate
// JSON keys (encoding/json behaviour) — and, critically, no panic.
func TestFromOCSFJSON_DuplicateKeys(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NotPanics(t, func() {
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"count":1,"count":2}`), &evt))
	})
	require.Equal(t, int32(2), evt.Count, "duplicate keys: last one wins")
}

// TestFromOCSFJSON_NullArrayElement verifies a null inside an array errors
// cleanly (OCSF arrays carry no nulls; silently dropping would corrupt data).
func TestFromOCSFJSON_NullArrayElement(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NotPanics(t, func() {
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"tags":["a",null,"b"]}`), &evt))
	})
}

// TestFromOCSFJSON_TruncationSweep exports a fully populated event, then
// parses EVERY prefix of the JSON. No prefix may panic; every strict prefix
// must error (the only valid JSON document in the sweep is the full one).
func TestFromOCSFJSON_TruncationSweep(t *testing.T) {
	b, err := exporter.ToOCSFJSON(fullSample(t))
	require.NoError(t, err)

	for i := range len(b) {
		prefix := b[:i]
		var evt samplev1.SampleEvent
		require.NotPanics(t, func() {
			err := exporter.FromOCSFJSON(prefix, &evt)
			require.Error(t, err, "truncated JSON prefix of length %d must be rejected", i)
		})
	}

	// The full document still parses.
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON(b, &evt))
}

// TestFromOCSFJSON_MutationSweep flips each structural byte of valid JSON to
// a hostile character and asserts graceful handling (error or success, no
// panic, and on success a working re-export).
func TestFromOCSFJSON_MutationSweep(t *testing.T) {
	b, err := exporter.ToOCSFJSON(fullSample(t))
	require.NoError(t, err)

	for _, hostile := range []byte{'{', '}', '[', ']', '"', ',', ':', '\\', 0x00} {
		for i := range b {
			mutated := append([]byte(nil), b...)
			if mutated[i] == hostile {
				continue
			}
			mutated[i] = hostile

			var evt samplev1.SampleEvent
			require.NotPanics(t, func() {
				if err := exporter.FromOCSFJSON(mutated, &evt); err == nil {
					// Rare but possible (mutation inside a string literal).
					// Whatever decoded must re-export cleanly.
					_, exportErr := exporter.ToOCSFJSON(&evt)
					require.NoError(t, exportErr)
				}
			}, "mutation at byte %d to %q must not panic", i, hostile)
		}
	}
}

// TestFromOCSFJSON_UnicodeEscapes probes JSON escape handling in keys and
// values.
func TestFromOCSFJSON_UnicodeEscapes(t *testing.T) {
	t.Run("EscapedKeyResolvesToField", func(t *testing.T) {
		// "time" unescapes to "time": it must set the field, not land in
		// unknown keys.
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"tim\u0065":42}`), &evt))
		require.Equal(t, int64(42), evt.Time)
	})

	t.Run("ValidSurrogatePair", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"message_text":"😀"}`), &evt))
		require.Equal(t, "😀", evt.MessageText)

		b, err := exporter.ToOCSFJSON(&evt)
		require.NoError(t, err)
		var again samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON(b, &again))
		require.True(t, proto.Equal(&evt, &again))
	})

	t.Run("LoneSurrogate", func(t *testing.T) {
		// encoding/json replaces a lone surrogate with U+FFFD. That input is
		// not byte-round-trippable by definition; what matters is that the
		// decode result is valid UTF-8 and re-exports cleanly.
		var evt samplev1.SampleEvent
		require.NotPanics(t, func() {
			if err := exporter.FromOCSFJSON([]byte(`{"message_text":"\ud800"}`), &evt); err == nil {
				require.True(t, utf8.ValidString(evt.MessageText),
					"decoded lone surrogate must be replaced with valid UTF-8")
				_, exportErr := exporter.ToOCSFJSON(&evt)
				require.NoError(t, exportErr)
			}
		})
	})
}

// TestFromOCSFJSON_ExoticDocuments covers document-level oddities: BOM,
// unusual whitespace, comments, concatenated documents.
func TestFromOCSFJSON_ExoticDocuments(t *testing.T) {
	t.Run("BOMPrefix", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte("\xef\xbb\xbf{\"time\":1}"), &evt),
			"a UTF-8 BOM is not valid JSON and must be rejected")
	})

	t.Run("ExoticWhitespace", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte("\r\n\t {\r\n\t\"time\" :\t1 ,\n\"count\": 2\r}\n"), &evt))
		require.Equal(t, int64(1), evt.Time)
		require.Equal(t, int32(2), evt.Count)
	})

	t.Run("Comments", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{/*c*/"time":1}`), &evt))
		require.Error(t, exporter.FromOCSFJSON([]byte("{\"time\":1} // trailing"), &evt))
	})

	t.Run("ConcatenatedDocuments", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"time":1}{"time":2}`), &evt),
			"trailing second document must be rejected, not silently ignored")
	})
}

// TestFromOCSFJSON_MoreNumberFormats extends the numeric-edge coverage with
// JSON-spec oddities.
func TestFromOCSFJSON_MoreNumberFormats(t *testing.T) {
	t.Run("NegativeZeroInt", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"time":-0}`), &evt))
		require.Zero(t, evt.Time)
	})

	t.Run("FloatValuedEnum", func(t *testing.T) {
		// 5.0 is numerically 5 but not an integer literal; OCSF enums are
		// integers, so this is rejected rather than silently coerced.
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"severity_id":5.0}`), &evt))
	})

	t.Run("DoubleOverflow", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NotPanics(t, func() {
			require.Error(t, exporter.FromOCSFJSON([]byte(`{"score":1e309}`), &evt),
				"a float64 out-of-range literal must error, not become +Inf")
		})
	})

	t.Run("ExponentIntoDouble", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON([]byte(`{"score":1.5e2}`), &evt))
		require.Equal(t, 150.0, evt.Score)
	})

	t.Run("PlusSign", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"time":+1}`), &evt))
	})

	t.Run("HexLiteral", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"time":0x10}`), &evt))
	})

	t.Run("BareNaNLiterals", func(t *testing.T) {
		var evt samplev1.SampleEvent
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"score":NaN}`), &evt))
		require.Error(t, exporter.FromOCSFJSON([]byte(`{"score":Infinity}`), &evt))
	})
}

// TestFromOCSFJSON_ExplicitZerosCanonicalize documents the canonical-form
// contract for proto3 implicit-presence fields: explicit zero values are
// accepted on import but omitted on export (the exporter emits set fields
// only), so JSON→proto→JSON byte-identity holds exactly for canonical inputs.
func TestFromOCSFJSON_ExplicitZerosCanonicalize(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON(
		[]byte(`{"count":0,"is_alert":false,"message_text":"","time":1}`), &evt))
	require.True(t, proto.Equal(&samplev1.SampleEvent{Time: 1}, &evt),
		"explicit zeros must decode to the zero value")

	b, err := exporter.ToOCSFJSON(&evt)
	require.NoError(t, err)
	require.Equal(t, `{"time":1}`, string(b),
		"re-export must canonicalize: implicit-presence zeros are omitted")
}

// TestFromOCSFJSON_OptionalPresenceRoundTrip verifies explicit-presence
// (optional) fields keep the set-to-zero vs absent distinction through both
// directions.
func TestFromOCSFJSON_OptionalPresenceRoundTrip(t *testing.T) {
	var evt samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON([]byte(`{"note":""}`), &evt))
	require.NotNil(t, evt.Note, "optional field set to empty string must have presence")
	require.Empty(t, *evt.Note)

	b, err := exporter.ToOCSFJSON(&evt)
	require.NoError(t, err)
	require.Equal(t, `{"note":""}`, string(b),
		"present-but-empty optional must survive export")

	var again samplev1.SampleEvent
	require.NoError(t, exporter.FromOCSFJSON(b, &again))
	require.True(t, proto.Equal(&evt, &again))
}

// TestFromOCSFJSON_DeepArrayNesting mirrors the deep-object test with arrays.
func TestFromOCSFJSON_DeepArrayNesting(t *testing.T) {
	const depth = 1000
	payload := `{"metadata":` + strings.Repeat(`[`, depth) + `1` + strings.Repeat(`]`, depth) + `}`

	var evt samplev1.SampleEvent
	require.NotPanics(t, func() {
		if err := exporter.FromOCSFJSON([]byte(payload), &evt); err == nil {
			_, exportErr := exporter.ToOCSFJSON(&evt)
			require.NoError(t, exportErr)
		}
	})
}

// TestRoundTrip_Concurrent exercises the exporter and importer from many
// goroutines to surface shared-state bugs under the race detector.
func TestRoundTrip_Concurrent(t *testing.T) {
	const goroutines = 8
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := newSeededRand(uint64(g) + 1)
			for range 50 {
				original := randomSampleEvent(t, rng)
				b, err := exporter.ToOCSFJSON(original)
				if err != nil {
					t.Errorf("goroutine %d: export: %v", g, err)
					return
				}
				var decoded samplev1.SampleEvent
				if err := exporter.FromOCSFJSON(b, &decoded); err != nil {
					t.Errorf("goroutine %d: import: %v", g, err)
					return
				}
				if !proto.Equal(original, &decoded) {
					t.Errorf("goroutine %d: round-trip mismatch", g)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRoundTrip_RandomizedEvents is the property-based smoke test: many
// randomized (seeded, reproducible) events must round-trip proto → JSON →
// proto losslessly and JSON → proto → JSON byte-identically.
func TestRoundTrip_RandomizedEvents(t *testing.T) {
	const iterations = 300
	rng := newSeededRand(0x0C5F_BEEF) // fixed seed: failures reproduce exactly

	for i := range iterations {
		original := randomSampleEvent(t, rng)

		b, err := exporter.ToOCSFJSON(original)
		require.NoError(t, err, "iteration %d: export must succeed\nevent: %v", i, original)

		var decoded samplev1.SampleEvent
		require.NoError(t, exporter.FromOCSFJSON(b, &decoded),
			"iteration %d: import must succeed\njson: %s", i, b)
		require.True(t, proto.Equal(original, &decoded),
			"iteration %d: proto → JSON → proto must be lossless\njson: %s", i, b)

		b2, err := exporter.ToOCSFJSON(&decoded)
		require.NoError(t, err)
		require.Equal(t, string(b), string(b2),
			"iteration %d: JSON → proto → JSON must be byte-identical", i)
	}
}

// FuzzFromOCSFJSON is the fuzz target for the importer: arbitrary bytes must
// never panic, and any input that decodes must satisfy the round-trip
// invariants (re-export succeeds, re-import is proto.Equal, second export is
// byte-identical — i.e. import ∘ export is idempotent).
//
// `go test` runs the seed corpus as a smoke test on every CI run;
// `go test -fuzz=FuzzFromOCSFJSON ./internal/ocsf/exporter/` fuzzes for real.
func FuzzFromOCSFJSON(f *testing.F) {
	// Seeds: valid documents and near-valid hostile ones.
	seeds := []string{
		`{}`,
		`{"time":1751313600000,"severity_id":5}`,
		`{"severity_id":99,"count":-1,"message_text":"x","is_alert":true,"score":1.5}`,
		`{"tags":["a","b"],"severities":[5,99],"actor":{"name":"alice","uid":9007199254740993}}`,
		`{"metadata":{"nested":{"deep":[1,2,3]}}}`,
		`{"note":"opt","metadata":null}`,
		`{"unknown_key":"value"}`,
		`{"time":9223372036854775807}`,
		`{"time":9223372036854775808}`,
		`{"time":"1751313600000"}`,
		`{"time":1.5}`,
		`{"actor":"not-an-object"}`,
		`{"tags":[null]}`,
		`[]`,
		`null`,
		`{"a":`,
		"{\"ti\x00me\":1}",
		`{"message_text":"\ud800"}`,        // lone surrogate
		"\xef\xbb\xbf{\"time\":1}",         // BOM prefix
		`{"score":1e309}`,                  // float64 overflow
		`{"time":1}{"time":2}`,             // concatenated documents
		`{"tim\u0065":42,"not\u0065":"x"}`, // escaped keys
		`{"count":-0,"severity_id":5.0}`,   // negative zero + float enum
	}
	// A real exported event as a seed.
	full := &samplev1.SampleEvent{
		SeverityId:  samplev1.SampleEvent_SEVERITY_ID_CRITICAL,
		Time:        1751313600000,
		Count:       42,
		MessageText: "hello",
		IsAlert:     true,
		Score:       99.5,
		Tags:        []string{"x", "y"},
		Actor:       &samplev1.Actor{Name: "alice", Uid: 7},
	}
	if b, err := exporter.ToOCSFJSON(full); err == nil {
		seeds = append(seeds, string(b))
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		var evt samplev1.SampleEvent
		if err := exporter.FromOCSFJSON(data, &evt); err != nil {
			return // rejected input is fine; panics are the failure mode
		}

		// Decoded successfully: the round-trip invariants must hold.
		b, err := exporter.ToOCSFJSON(&evt)
		if err != nil {
			t.Fatalf("decoded event failed to re-export: %v\ninput: %q", err, data)
		}
		var again samplev1.SampleEvent
		if err := exporter.FromOCSFJSON(b, &again); err != nil {
			t.Fatalf("re-exported JSON failed to re-import: %v\njson: %s", err, b)
		}
		if !proto.Equal(&evt, &again) {
			t.Fatalf("import ∘ export is not idempotent\nfirst:  %v\nsecond: %v\njson: %s", &evt, &again, b)
		}
		b2, err := exporter.ToOCSFJSON(&again)
		if err != nil {
			t.Fatalf("second export failed: %v", err)
		}
		if !bytes.Equal(b, b2) {
			t.Fatalf("export is not deterministic after round-trip\nfirst:  %s\nsecond: %s", b, b2)
		}
	})
}

// FuzzToOCSFJSON fuzzes the exporter through structured field values: no
// combination of scalar values may panic, and every successful export must
// re-import to an equal message.
func FuzzToOCSFJSON(f *testing.F) {
	f.Add(int64(0), int32(0), "", false, 0.0, int32(0))
	f.Add(int64(math.MaxInt64), int32(math.MaxInt32), "ünïcødé 🦀", true, math.MaxFloat64, int32(99))
	f.Add(int64(math.MinInt64), int32(math.MinInt32), "\x00\xff invalid utf8 \xc3\x28", false, math.SmallestNonzeroFloat64, int32(-1))

	f.Fuzz(func(t *testing.T, tm int64, count int32, text string, alert bool, score float64, sev int32) {
		evt := &samplev1.SampleEvent{
			Time:        tm,
			Count:       count,
			MessageText: text,
			IsAlert:     alert,
			Score:       score,
			SeverityId:  samplev1.SampleEvent_SeverityId(sev),
		}

		b, err := exporter.ToOCSFJSON(evt)
		if err != nil {
			// Non-finite floats and invalid UTF-8 strings are legitimately
			// unexportable; rejection is correct as long as it doesn't panic.
			return
		}

		var decoded samplev1.SampleEvent
		if err := exporter.FromOCSFJSON(b, &decoded); err != nil {
			t.Fatalf("exported JSON failed to import: %v\njson: %s", err, b)
		}
		if !proto.Equal(evt, &decoded) {
			t.Fatalf("round-trip mismatch\noriginal: %v\ndecoded:  %v\njson: %s", evt, &decoded, b)
		}
	})
}

// ─── Randomized event generator ───────────────────────────────────────────────

// seededRand is a tiny deterministic PRNG (xorshift64*) so the property test
// is reproducible without importing math/rand's global state.
type seededRand struct{ state uint64 }

func newSeededRand(seed uint64) *seededRand {
	if seed == 0 {
		seed = 1
	}
	return &seededRand{state: seed}
}

func (r *seededRand) next() uint64 {
	r.state ^= r.state >> 12
	r.state ^= r.state << 25
	r.state ^= r.state >> 27
	return r.state * 0x2545F4914F6CDD1D
}

func (r *seededRand) intn(n int) int { return int(r.next() % uint64(n)) }

func (r *seededRand) chance(pct int) bool { return r.intn(100) < pct }

// randomString draws from a pool of hostile-ish content.
func (r *seededRand) randomString() string {
	pool := []string{
		"", "plain", "ünïcødé 日本語 🦀", `with "quotes" and \backslashes\`,
		"new\nline\ttab", "trailing space ", " leading",
		`{"looks":"like json"}`, "null", "-1", "1e9",
		strings.Repeat("long", 256),
	}
	return pool[r.intn(len(pool))]
}

func (r *seededRand) randomInt64() int64 {
	pool := []int64{0, 1, -1, math.MaxInt64, math.MinInt64, 1 << 53, (1 << 53) + 1, 1751313600000}
	return pool[r.intn(len(pool))]
}

func (r *seededRand) randomInt32() int32 {
	pool := []int32{0, 1, -1, math.MaxInt32, math.MinInt32, 99}
	return pool[r.intn(len(pool))]
}

// randomFinite returns a finite float64 (NaN/Inf are unexportable by design
// and covered separately).
func (r *seededRand) randomFinite() float64 {
	pool := []float64{0, 1.5, -1.5, math.MaxFloat64, math.SmallestNonzeroFloat64, 1e-10, 99.999}
	return pool[r.intn(len(pool))]
}

// randomValue builds a random google.protobuf.Value up to depth 3.
func (r *seededRand) randomValue(t *testing.T, depth int) *structpb.Value {
	t.Helper()
	if depth <= 0 || r.chance(40) {
		switch r.intn(4) {
		case 0:
			return structpb.NewStringValue(r.randomString())
		case 1:
			return structpb.NewNumberValue(r.randomFinite())
		case 2:
			return structpb.NewBoolValue(r.chance(50))
		default:
			return structpb.NewNullValue()
		}
	}
	if r.chance(50) {
		fields := map[string]*structpb.Value{}
		for i := range r.intn(4) {
			fields[fmt.Sprintf("k%d", i)] = r.randomValue(t, depth-1)
		}
		return structpb.NewStructValue(&structpb.Struct{Fields: fields})
	}
	var vals []*structpb.Value
	for range r.intn(4) {
		vals = append(vals, r.randomValue(t, depth-1))
	}
	return structpb.NewListValue(&structpb.ListValue{Values: vals})
}

// randomSampleEvent populates a random subset of SampleEvent's fields with
// random (frequently adversarial) values.
func randomSampleEvent(t *testing.T, r *seededRand) *samplev1.SampleEvent {
	t.Helper()
	evt := &samplev1.SampleEvent{}

	if r.chance(70) {
		evt.Time = r.randomInt64()
	}
	if r.chance(70) {
		evt.Count = r.randomInt32()
	}
	if r.chance(70) {
		evt.MessageText = r.randomString()
	}
	if r.chance(50) {
		evt.IsAlert = true
	}
	if r.chance(60) {
		evt.Score = r.randomFinite()
	}
	if r.chance(60) {
		evt.SeverityId = samplev1.SampleEvent_SeverityId(r.randomInt32())
	}
	if r.chance(50) {
		n := r.intn(4)
		for range n {
			evt.Tags = append(evt.Tags, r.randomString())
		}
	}
	if r.chance(50) {
		n := r.intn(3)
		for range n {
			evt.Severities = append(evt.Severities, samplev1.SampleEvent_SeverityId(r.randomInt32()))
		}
	}
	if r.chance(50) {
		evt.Actor = &samplev1.Actor{Name: r.randomString(), Uid: r.randomInt64()}
	}
	if r.chance(40) {
		note := r.randomString()
		evt.Note = &note
	}
	if r.chance(40) {
		evt.Metadata = r.randomValue(t, 3)
	}
	return evt
}

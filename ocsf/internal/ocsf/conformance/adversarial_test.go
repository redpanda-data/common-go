// Copyright 2026 Redpanda Data, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

// Adversarial smoke tests for the JSON ↔ proto transformation on the REAL
// generated classes (ApiActivity, EntityManagement, AuditEvent), complementing
// the sample-message adversarial suite in the exporter package.
package conformance_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ocsfv1 "github.com/redpanda-data/common-go/ocsf/internal/ocsf/conformance/genpb/ocsf/v1"
	"github.com/redpanda-data/common-go/ocsf/internal/ocsf/exporter"
)

// TestAdversarial_TruncationSweep parses every prefix of a real exported
// event: no prefix may panic, every strict prefix must error.
func TestAdversarial_TruncationSweep(t *testing.T) {
	events := map[string]proto.Message{
		"ApiActivity": buildApiActivityAllowed(),
		"AuditEvent":  buildMergedApiActivityAllowed(),
	}

	for name, evt := range events {
		t.Run(name, func(t *testing.T) {
			b, err := exporter.ToOCSFJSON(evt)
			require.NoError(t, err)

			for i := range len(b) {
				prefix := b[:i]
				var decoded ocsfv1.AuditEvent
				require.NotPanics(t, func() {
					require.Error(t, exporter.FromOCSFJSON(prefix, &decoded),
						"truncated prefix of length %d must be rejected", i)
				})
			}
		})
	}
}

// TestAdversarial_MutationSweep corrupts each byte of real event JSON with
// structural characters: never a panic; on the rare accidental-valid result,
// the decoded event must still export.
func TestAdversarial_MutationSweep(t *testing.T) {
	b, err := exporter.ToOCSFJSON(buildMergedApiActivityAllowed())
	require.NoError(t, err)

	for _, hostile := range []byte{'{', '}', '[', ']', '"', ',', ':', 0x00} {
		for i := range b {
			if b[i] == hostile {
				continue
			}
			mutated := append([]byte(nil), b...)
			mutated[i] = hostile

			var evt ocsfv1.AuditEvent
			require.NotPanics(t, func() {
				if err := exporter.FromOCSFJSON(mutated, &evt); err == nil {
					_, exportErr := exporter.ToOCSFJSON(&evt)
					require.NoError(t, exportErr)
				}
			}, "mutation at byte %d to %q must not panic", i, hostile)
		}
	}
}

// TestAdversarial_HostileFieldContent round-trips real events carrying
// hostile string content through the merged message.
func TestAdversarial_HostileFieldContent(t *testing.T) {
	hostile := []string{
		"ünïcødé 日本語 🦀",
		`","class_uid":9999,"x":"`, // JSON injection attempt
		"line\nbreaks\tand\rreturns",
		strings.Repeat("Z", 1<<18), // 256 KiB message
		`<img src=x onerror=alert(1)>`,
		"",
	}

	for i, s := range hostile {
		evt := buildMergedApiActivityAllowed()
		evt.Message = s
		evt.Actor.User.Name = s

		b, err := exporter.ToOCSFJSON(evt)
		require.NoError(t, err, "case %d must export", i)

		var decoded ocsfv1.AuditEvent
		require.NoError(t, exporter.FromOCSFJSON(b, &decoded), "case %d must import", i)
		require.True(t, proto.Equal(evt, &decoded), "case %d must round-trip losslessly", i)

		// The injection attempt must not have altered the class discriminator.
		require.Equal(t, ocsfv1.AuditEvent_CLASS_UID_API_ACTIVITY, decoded.ClassUid)
	}
}

// TestAdversarial_NullUnmappedWithUnknownKeys is the regression test for a
// panic found by FuzzFromOCSFJSON_AuditEvent: an explicit `"unmapped":null`
// combined with unknown keys must merge cleanly (json.Unmarshal of null nils
// the merge map without error, which previously crashed maps.Copy).
func TestAdversarial_NullUnmappedWithUnknownKeys(t *testing.T) {
	var evt ocsfv1.AuditEvent
	require.NotPanics(t, func() {
		require.NoError(t, exporter.FromOCSFJSON(
			[]byte(`{"class_uid":6003,"unmapped":null,"future_attr":1}`), &evt,
		))
	})
	require.NotNil(t, evt.Unmapped)
	un := evt.Unmapped.GetStructValue()
	require.NotNil(t, un, "unknown keys must replace the null unmapped")
	require.Equal(t, float64(1), un.Fields["future_attr"].GetNumberValue())
}

// TestAdversarial_RandomizedAuditEvents is the property smoke test on the
// merged message: seeded random events across BOTH classes must round-trip
// proto → JSON → proto losslessly and JSON → proto → JSON byte-identically.
func TestAdversarial_RandomizedAuditEvents(t *testing.T) {
	const iterations = 200
	rng := newConformanceRand(0xA0D17_EE7)

	for i := range iterations {
		original := randomAuditEvent(rng)

		b, err := exporter.ToOCSFJSON(original)
		require.NoError(t, err, "iteration %d: export must succeed", i)

		var decoded ocsfv1.AuditEvent
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

// FuzzFromOCSFJSON_AuditEvent fuzzes the importer against the real merged
// message. Seed corpus includes full exported events of both classes; the
// invariant is the same as the sample fuzz target: no panic, and decodable
// input must round-trip idempotently.
func FuzzFromOCSFJSON_AuditEvent(f *testing.F) {
	for _, evt := range []proto.Message{
		buildMergedApiActivityAllowed(),
		buildMergedEntityManagement(),
	} {
		if b, err := exporter.ToOCSFJSON(evt); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`{"class_uid":6003,"unknown_future_attr":{"deep":[1,2]}}`))
	f.Add([]byte(`{"class_uid":3004,"entity":null,"unmapped":null}`))
	f.Add([]byte(`{"activity_id":99,"type_uid":600399}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var evt ocsfv1.AuditEvent
		if err := exporter.FromOCSFJSON(data, &evt); err != nil {
			return
		}
		b, err := exporter.ToOCSFJSON(&evt)
		if err != nil {
			t.Fatalf("decoded event failed to re-export: %v\ninput: %q", err, data)
		}
		var again ocsfv1.AuditEvent
		if err := exporter.FromOCSFJSON(b, &again); err != nil {
			t.Fatalf("re-exported JSON failed to re-import: %v\njson: %s", err, b)
		}
		if !proto.Equal(&evt, &again) {
			t.Fatalf("import ∘ export is not idempotent\njson: %s", b)
		}
	})
}

// ─── Randomized AuditEvent generator ─────────────────────────────────────────

// conformanceRand is a deterministic xorshift64* PRNG for reproducible
// property tests.
type conformanceRand struct{ state uint64 }

func newConformanceRand(seed uint64) *conformanceRand {
	if seed == 0 {
		seed = 1
	}
	return &conformanceRand{state: seed}
}

func (r *conformanceRand) next() uint64 {
	r.state ^= r.state >> 12
	r.state ^= r.state << 25
	r.state ^= r.state >> 27
	return r.state * 0x2545F4914F6CDD1D
}

func (r *conformanceRand) intn(n int) int      { return int(r.next() % uint64(n)) }
func (r *conformanceRand) chance(pct int) bool { return r.intn(100) < pct }

func (r *conformanceRand) str() string {
	pool := []string{
		"", "alice", "user:alice@example.com", "ünïcødé 🦀", `"quoted"`,
		"multi\nline", "policy:x/y:z", strings.Repeat("k", 512),
	}
	return pool[r.intn(len(pool))]
}

// randomAuditEvent builds a random event of a random class. It intentionally
// does NOT keep the class-consistency invariants (protovalidate's job); the
// round-trip must be lossless regardless of semantic validity.
func randomAuditEvent(r *conformanceRand) *ocsfv1.AuditEvent {
	evt := &ocsfv1.AuditEvent{}

	if r.chance(80) {
		if r.chance(50) {
			evt.ClassUid = ocsfv1.AuditEvent_CLASS_UID_API_ACTIVITY
			evt.CategoryUid = ocsfv1.AuditEvent_CATEGORY_UID_APPLICATION_ACTIVITY
		} else {
			evt.ClassUid = ocsfv1.AuditEvent_CLASS_UID_ENTITY_MANAGEMENT
			evt.CategoryUid = ocsfv1.AuditEvent_CATEGORY_UID_IDENTITY_ACCESS_MANAGEMENT
		}
	}
	if r.chance(80) {
		evt.ActivityId = int32(r.intn(100))
	}
	if r.chance(80) {
		evt.TypeUid = ocsfv1.AuditEvent_TypeUid(int32(r.intn(700000)))
	}
	if r.chance(80) {
		evt.Time = int64(r.next() >> 1)
	}
	if r.chance(70) {
		evt.SeverityId = ocsfv1.AuditEvent_SeverityId(r.intn(100))
	}
	if r.chance(60) {
		evt.Message = r.str()
	}
	if r.chance(60) {
		evt.StatusId = ocsfv1.AuditEvent_StatusId(r.intn(100))
	}
	if r.chance(50) {
		evt.DispositionId = ocsfv1.AuditEvent_DispositionId(r.intn(100))
	}

	populateRandomObjects(r, evt)
	return evt
}

// populateRandomObjects fills a random subset of the event's message-typed
// fields (actor, api, entity, metadata, authorizations, endpoints, cloud).
func populateRandomObjects(r *conformanceRand, evt *ocsfv1.AuditEvent) {
	if r.chance(60) {
		evt.Actor = &ocsfv1.Actor{
			User: &ocsfv1.User{Uid: r.str(), Name: r.str()},
		}
	}
	if r.chance(50) {
		evt.Api = &ocsfv1.Api{Operation: r.str()}
	}
	if r.chance(40) {
		evt.Entity = &ocsfv1.ManagedEntity{
			Uid:    r.str(),
			Name:   r.str(),
			TypeId: ocsfv1.ManagedEntity_TypeId(r.intn(100)),
		}
	}
	if r.chance(50) {
		evt.Metadata = &ocsfv1.Metadata{
			Version:  "1.8.0",
			Profiles: []string{"cloud"},
			Product:  &ocsfv1.Product{Name: r.str(), VendorName: r.str()},
		}
	}
	if r.chance(40) {
		n := r.intn(3)
		for range n {
			evt.Authorizations = append(evt.Authorizations, &ocsfv1.Authorization{
				Decision: r.str(),
				Policy:   &ocsfv1.Policy{Uid: r.str()},
			})
		}
	}
	if r.chance(30) {
		evt.SrcEndpoint = &ocsfv1.NetworkEndpoint{Ip: "10.0.0.1", Port: int32(r.intn(65536))}
	}
	if r.chance(30) {
		evt.Cloud = &ocsfv1.Cloud{Provider: r.str(), Region: r.str()}
	}
}

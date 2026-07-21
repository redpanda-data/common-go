## OCSF

`ocsf` turns the compiled [OCSF](https://schema.ocsf.io) (Open Cybersecurity
Schema Framework) security-event schema into wire-stable proto3, and marshals
generated events to schema-valid OCSF JSON.

### Why

OCSF ships no protobuf binding, and the schema is versioned JSON. This module
generates proto3 for a chosen set of OCSF classes with **stable field numbers**
across schema versions, so events can be stored and streamed as proto and
exported as OCSF JSON at the boundary.

### What a run produces

Two artifacts are the point of this module; two more keep them safe to evolve.

1. **Proto source files** under `ocsf/v<N>/`: one message per selected class
   plus the merged single-event message, all sharing `objects.proto`. Fields
   carry wire-stable numbers from the tagmap and `buf.validate` annotations
   (required levels, plus class-aware CEL rules on the merged message), so
   protovalidate enforces OCSF semantics on every event before it is
   published. With `--merged-sr-subject`, the merged message also carries the
   `(redpanda.api.common.v1.schema_registry)` annotation that drives
   `protoc-gen-go-sr-normalize`.
2. **Generated Go code**, produced from those protos with `buf generate` and
   `protoc-gen-go`. The bindings committed under
   `internal/ocsf/conformance/genpb/` are the reference: `buf.gen.yaml` and
   `buf.gen.merged.yaml` in that directory hold the exact recipe. This module
   does not export event types; consumers run the same recipe in their own
   repo, pinned to their OCSF version and class selection, and get identical
   wire-compatible types.
3. **Schema Registry schemas** (`<name>.sr.proto`): self-contained copies with
   no imports and no custom options, event message at Confluent index 0.
   Register as-is. Each one gets a `<name>.sr.go` companion embedding the
   schema text (and, for the merged message, the subject) as Go constants
   (`<MessageName>SRSchema`, `<MessageName>SRSubject`), so consumers never
   hand-embed the proto text.
4. **The tagmap** (`field-numbers.json`): the append-only field-number map
   that keeps every regeneration wire-compatible with the previous one,
   enforced in CI by `--compat-check`.
5. With `--iceberg-compat`: **the prune sidecar**
   (`ocsf/v<N>/iceberg-compat-prunes.txt`), one sorted
   `<Message>.<field> <rule>` line per field dropped from the emitted protos,
   so fidelity loss is diffable across OCSF versions.

### Layouts

The generator emits two layouts from one tagmap:

- **Per-class** (always): one message per class (`ApiActivity`,
  `EntityManagement`, ...), one file each, plus a shared `objects.proto`.
- **Merged single-event** (`--merged-message AuditEvent`): ONE flat message
  holding the union of all selected classes' attributes, for a single
  audit-log topic carrying every class. Shared base_event attributes appear
  once; class-scoped enums (`activity_id`) are demoted to plain scalars
  because their values mean different things per class (semantics live in the
  `(class_uid, activity_id)` pair and the unioned `TypeUid` enum); class-aware
  validation is generated as protovalidate CEL: `type_uid == class_uid * 100 +
  activity_id`, per-class field ownership, and per-class requiredness. A
  merged event exports OCSF JSON identical to the per-class message.

### Packages

- `internal/ocsf/schema` — loads the OCSF `export/v2` compiled schema.
- `internal/ocsf/gen` — maps OCSF types/enums to proto3 and emits deterministic
  `.proto` with protovalidate; `MergeClasses`/`EmitMerged` produce the
  single-event layout.
- `internal/ocsf/tagmap` — the append-only `(message, attribute) -> tag` map and
  its compatibility check (the wire-stability guarantee).
- `internal/ocsf/exporter` — `ToOCSFJSON` marshals a generated proto message to
  OCSF JSON (integer enums, numeric int64, snake_case keys); `FromOCSFJSON` is
  its exact inverse (unknown keys land in `unmapped`), so events round-trip
  proto → JSON → proto losslessly and JSON → proto → JSON byte-identically.
- `cmd/ocsf-protogen` — the generator CLI.

### CLI

```
go run ./cmd/ocsf-protogen \
  --schema  internal/ocsf/schema/testdata/ocsf-1.8.0.json \
  --classes api_activity,entity_management \
  --version 1.8.0 \
  --out     out-dir \
  --tagmap  field-numbers.json \
  --merged-message AuditEvent \
  --sr-schema-out sr-dir
```

- `--merged-message <Name>` additionally emits the merged single-event layout
  (`ocsf/v<N>/<snake_name>.proto`, and `<snake_name>.sr.proto` under
  `--sr-schema-out`).
- `--merged-sr-subject <subject>` annotates the merged message with
  `(redpanda.api.common.v1.schema_registry) = { subject: "..." }` (from
  `buf.build/redpandadata/common`). Consumers that run
  [`protoc-gen-go-sr-normalize`](../protoc-gen-go-sr-normalize) in their buf
  pass then get the self-contained SR schema and subject as Go constants
  (`<Name>SRSchema`, `<Name>SRSubject`) with no hand-copied proto. Requires
  `--merged-message`.
- `--sr-schema-out <dir>` writes self-contained Schema-Registry schemas (no
  imports to resolve; register as-is, event message at Confluent index 0),
  each with a `<name>.sr.go` companion embedding the schema as Go constants.
  This is the non-buf path to the same schema text; Go consumers should
  prefer the annotation plus `protoc-gen-go-sr-normalize`.
- `--sr-go-package <name>` sets the Go package of the `.sr.go` companions.
  Default is derived from the OCSF major version: `1.8.0` → `ocsfv1`.
- `--iceberg-compat` prunes the schema model before emission so the merged
  event topic can be Iceberg-enabled and queried from Oxla (see below).
- `--check` regenerates and fails on output drift or newly-stubbed objects.
- `--compat-check --old <a> --new <b>` fails if field numbers changed
  incompatibly between two tag maps (used in CI against the base branch).

### `--iceberg-compat`

Redpanda topics with `redpanda.iceberg.mode=value_schema_id_prefix` are
translated proto → Iceberg by the broker and read from Oxla (Redpanda SQL).
Verified against a live stack, the unpruned schema fails that pipeline on
three independent grounds, each fixed by a deterministic prune rule (plus one
hygiene rule) applied to the loaded model BEFORE emission — so the per-class
protos, the merged message, the SR schemas, and validation all agree on one
shape. All pruned
fields are ones the downstream emitters never populate; pruning uniformly
(rather than only in the SR schema) keeps the Go bindings, the wire schema,
and the analytics schema identical, so nothing can be populated that the
Iceberg/Oxla side cannot represent.

- **R1 — well-known types.** Oxla cannot load protobuf well-known types;
  `CREATE TABLE` on the source fails with:

  ```
  Failed to build proto file descriptor: google/protobuf/struct.proto: Import
  "google/protobuf/struct.proto" has not been loaded.;
  ocsf.v1.AuditEvent.unmapped: ".google.protobuf.Value" is not defined.
  ```

  Every field mapping to `google.protobuf.Value` (OCSF `json_t` and the
  generic `object` bag) is dropped; the generated files then import
  `google/protobuf/struct.proto` nowhere.

- **R2 — recursion.** Redpanda's proto→Iceberg translator rejects recursive
  proto types and DLQs every record:

  ```
  Protobuf schema translation failed: iceberg::conversion_exception
  (Protocol buffer field not supported - recursive type detected, type
  hierarchy: ocsf.v1.AuditEvent, ocsf.v1.Actor, ocsf.v1.Idp,
  ocsf.v1.AuthFactor, ocsf.v1.Device, ocsf.v1.User, ocsf.v1.LdapPerson,
  current type: message User {...
  ```

  A single depth-first walk over message-typed fields from each selected
  root class — roots in sorted class-name order, fields in sorted
  attribute-name order — drops every field whose target type is already on
  the current DFS path (self-references included). Removing all back edges
  of one DFS forest leaves the graph acyclic; a post-condition check fails
  generation if a cycle somehow survives.

- **R3 — identifier length.** Oxla's Iceberg leg names nested type aliases
  after the full dotted field path from the root message and enforces
  PostgreSQL's 63-char identifier limit on `REFRESH`:

  ```
  type alias 'actor.process.file.data_classification.discovery_details.occurrence_details'
  exceeds PostgreSQL name limit (75 > 63)
  ```

  Applied after R1+R2 (shorter graph, fewer paths), and ONLY to
  message-typed fields — scalars are never pruned. Message types are shared,
  so a deep never-populated embedding must not cost a shared type its fields
  at shallow, load-bearing embeddings (`actor.user.email_addr` must survive
  even though `User` is also embedded under
  `actor.process...service_dll_file.accessor`); the cut lands on the deep
  embedding edge instead. An edge is cut when its worst-case embedding
  cannot fit its target's irreducible suffix (the longest scalar the target
  forces) within 63 chars; exactly 63 is kept. A cut edge drops its whole
  subtree, and objects left without any surviving embedding are not emitted
  at all.

- **R4 — empty messages.** A message-typed field whose target type ends up
  with no surviving fields (everything inside was pruned by R1–R3) is
  dropped too, to a fixpoint: an empty nested message carries no
  information, and cutting the edge removes the empty type from the emit
  closure.

After pruning, the model is verified — every surviving dotted leaf path from
every root is at most 63 chars, no reachable message is empty, and R3/R4
removed only message-typed fields — and generation fails on any violation.

Pruned fields KEEP their tagmap entries (append-only semantics: never
renumbered, never removed) — they are simply absent from the emitted protos —
so `--compat-check` passes against a tagmap produced without the flag, and a
later non-pruned regeneration reuses the original numbers.

Pinned to OCSF 1.8.0. The schema is fetched from
`https://schema.ocsf.io/{version}/export/v2/schema`; generated events are
conformance-validated against `https://schema.ocsf.io/{version}/api/v2/validate`.

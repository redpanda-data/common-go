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
   published.
2. **Generated Go code**, produced from those protos with `buf generate` and
   `protoc-gen-go`. The bindings committed under
   `internal/ocsf/conformance/genpb/` are the reference: `buf.gen.yaml` and
   `buf.gen.merged.yaml` in that directory hold the exact recipe. This module
   does not export event types; consumers run the same recipe in their own
   repo, pinned to their OCSF version and class selection, and get identical
   wire-compatible types.
3. **Schema Registry schemas** (`<name>.sr.proto`): self-contained copies with
   no imports and no custom options, event message at Confluent index 0.
   Register as-is.
4. **The tagmap** (`field-numbers.json`): the append-only field-number map
   that keeps every regeneration wire-compatible with the previous one,
   enforced in CI by `--compat-check`.

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
- `--sr-schema-out <dir>` writes self-contained Schema-Registry schemas (no
  imports to resolve; register as-is, event message at Confluent index 0).
- `--check` regenerates and fails on output drift or newly-stubbed objects.
- `--compat-check --old <a> --new <b>` fails if field numbers changed
  incompatibly between two tag maps (used in CI against the base branch).

Pinned to OCSF 1.8.0. The schema is fetched from
`https://schema.ocsf.io/{version}/export/v2/schema`; generated events are
conformance-validated against `https://schema.ocsf.io/{version}/api/v2/validate`.

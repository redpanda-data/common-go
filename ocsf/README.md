## OCSF

`ocsf` turns the compiled [OCSF](https://schema.ocsf.io) (Open Cybersecurity
Schema Framework) security-event schema into wire-stable proto3, and marshals
generated events to schema-valid OCSF JSON.

### Why

OCSF ships no protobuf binding, and the schema is versioned JSON. This module
generates proto3 for a chosen set of OCSF classes with **stable field numbers**
across schema versions, so events can be stored and streamed as proto and
exported as OCSF JSON at the boundary.

### Packages

- `internal/ocsf/schema` — loads the OCSF `export/v2` compiled schema.
- `internal/ocsf/gen` — maps OCSF types/enums to proto3 and emits deterministic
  `.proto` with protovalidate.
- `internal/ocsf/tagmap` — the append-only `(message, attribute) -> tag` map and
  its compatibility check (the wire-stability guarantee).
- `internal/ocsf/exporter` — marshals a generated proto message to OCSF JSON
  (integer enums, numeric int64, snake_case keys).
- `cmd/ocsf-protogen` — the generator CLI.

### CLI

```
go run ./cmd/ocsf-protogen \
  --schema  internal/ocsf/schema/testdata/ocsf-1.8.0.json \
  --classes api_activity,entity_management \
  --version 1.8.0 \
  --out     out.proto \
  --tagmap  field-numbers.json
```

- `--check` regenerates and fails on output drift or newly-stubbed objects.
- `--compat-check --old <a> --new <b>` fails if field numbers changed
  incompatibly between two tag maps (used in CI against the base branch).

Pinned to OCSF 1.8.0. The schema is fetched from
`https://schema.ocsf.io/{version}/export/v2/schema`; generated events are
conformance-validated against `https://schema.ocsf.io/{version}/api/v2/validate`.

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
   and, when requested, the merged single-event message, all sharing
   `objects.proto`. `--merged-only` suppresses the per-class files. Fields carry
   wire-stable numbers from the tagmap and `buf.validate` annotations (required
   levels, plus class-aware CEL rules on the merged message), so protovalidate
   enforces OCSF semantics on every event before it is published. With
   `--merged-sr-subject`, the merged message also carries the
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
   hand-embed the proto text. `--sr-go-only` emits only those Go companions
   when consumers do not need the intermediate `.sr.proto` files.
4. **The tagmap** (`field-numbers.json`): the append-only field-number map
   that keeps every regeneration wire-compatible with the previous one,
   enforced in CI by `--compat-check`.
5. With `--iceberg-compat`: **the prune sidecar**
   (`ocsf/v<N>/iceberg-compat-prunes.txt`), one sorted
   `<Message>.<field> <rule>` line per field dropped from the emitted protos,
   so fidelity loss is diffable across OCSF versions.
6. With `--mask-file`: **the read-mask report**
   (`ocsf/v<N>/read-mask-report.txt`), the resolved keep set plus every leaf
   column the mask kept without being asked to, so the published contract is
   reviewable as a diff.

### Layouts

The generator can emit two layouts from one tagmap:

- **Per-class** (default): one message per class (`ApiActivity`,
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

By default, `--merged-message` adds the merged layout alongside the per-class
layout. `--merged-only` emits only the merged message and `objects.proto`.

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
  --merged-only \
  --sr-schema-out sr-dir \
  --sr-go-only
```

- `--merged-message <Name>` additionally emits the merged single-event layout
  (`ocsf/v<N>/<snake_name>.proto`, and `<snake_name>.sr.proto` under
  `--sr-schema-out`).
- `--merged-only` suppresses the per-class proto and Schema Registry files,
  leaving the merged message and shared `objects.proto`. Requires
  `--merged-message`.
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
- `--sr-go-only` writes only the `.sr.go` schema embeds under
  `--sr-schema-out`, omitting their intermediate `.sr.proto` files. Requires
  `--sr-schema-out`.
- `--iceberg-compat` prunes the schema model before emission so the merged
  event topic can be Iceberg-enabled and queried from Oxla (see below).
- `--mask-file <path>` narrows the schema to an allowlist of attribute paths
  before anything else runs (see below).
- `--check` regenerates and fails on output drift or newly-stubbed objects.
- `--compat-check --old <a> --new <b>` fails if field numbers changed
  incompatibly between two tag maps (used in CI against the base branch).

### `--mask-file` (read mask)

An OCSF class is enormous: `api_activity` + `entity_management` reach ~100
message types and thousands of leaf columns, while a typical producer populates
a few dozen fields. Publishing the whole thing costs every consumer — and an
Iceberg-enabled topic pays for it per column on `REFRESH`.

`--mask-file` takes an allowlist of root-relative attribute paths and drops
everything else from the model before any emission, so the per-class protos, the
merged message, the SR schemas and the validation rules all describe the same
narrowed contract:

```yaml
version: 1
paths:
  - time
  - actor.user.email_addr
  - api.response.code
  - metadata.*
```

- A path ending in `.*` keeps a message-typed field's **whole subtree**
  (transitively). Any other path must end on a scalar; stopping on a
  message-typed field is an error that names the fix, so a mask cannot silently
  produce an empty message.
- The mask is **closed upward**: list leaves, not prefixes. `actor.user.email_addr`
  retains the `actor` and `actor.user` edges automatically.
- A path that does not resolve against the schema **fails generation**. That is
  the point of an allowlist: an OCSF rename stops matching, and CI says so
  instead of quietly dropping a column consumers query.
- Constraints (`at_least_one` / `just_one`) naming a dropped attribute are
  scrubbed, so the emitted CEL never references a field that no longer exists.
- With `--merged-message`, the class discriminators (`category_uid`,
  `class_uid`, `type_uid`) may not be masked away: the merged emitter gates its
  CEL on `class_uid` and readers demux the single topic on the trio.

**Type-scoped semantics.** Paths are written root-relative because that is how
consumers think about the event, but they are applied per message **type**:
`actor.user.email_addr` keeps `User.email_addr` wherever `User` is embedded. The
schema model is a graph of shared object types, so a per-path mask would mean
specialising a type per embedding path — a message explosion that would also
break the tagmap's `(message, attribute)` identity. The gap is reported rather
than hidden: every leaf column the output carries that no path asked for is
listed in the widening section of `read-mask-report.txt`. In practice the gap is
small, because sharing falls away as paths are closed — a type embedded at
fifteen paths in the full schema is usually reachable by one after masking.

**Wire stability.** Masking never renumbers. Tags are reserved over the
*unmasked* model before the mask is applied (`gen.ReserveTags`), so field numbers
are a function of the OCSF schema and the class selection alone — never of what
the mask chose to emit. Consequences:

- Adding a mask to an existing baseline leaves the committed tagmap
  byte-identical.
- A tagmap bootstrapped with a mask active is identical to one bootstrapped
  without it, so regenerating it is not destructive.
- **Un-masking a field later restores its original field number**, because the
  excluded attributes keep their recorded entries.

Starting narrow is therefore cheap and reversible.

> `field-numbers.json` must still be committed. Reserving is deterministic only
> for a *fixed* OCSF version: a new release that inserts an alphabetically-early
> attribute would shift every later number, and the committed tagmap is the only
> thing that prevents that (the new attribute takes the lowest free number
> instead). It is also the only record of `reserved` numbers retired fields must
> never give up. CI's `--compat-check` against the base branch is the gate.

**Ordering.** The mask runs before `--iceberg-compat`, and that ordering pays:
the prune rules react to the shape of the model (R3 demotes an edge because a
dotted path overflows 63 chars, R4 drops edges to emptied types), so removing
the deep subtrees first means far fewer fields have to collapse into JSON
strings. On the OCSF 1.8.0 fixture the mask above takes `--iceberg-compat` from
55 demotions to 3.

The mask itself is consumer policy, not generator knowledge: which fields you
publish belongs in your repo next to your committed output, the same way
`--classes` and `--merged-message` do.

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

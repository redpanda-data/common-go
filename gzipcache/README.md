## gzipcache

A gzip compressor for [connect-go](https://github.com/connectrpc/connect-go)
that compresses each distinct payload once.

A server that broadcasts one message to many streams (watch APIs, pub/sub
fanout) pays one deflate per stream with connect's built-in gzip. With 52
subscribers and a 2.2 MB message, that is 52 identical compressions per
publish. gzipcache keys every payload by its content: the first send
compresses it, concurrent sends of the same bytes wait for that result, and
later sends copy the cached frame.

### Usage

```go
cache, err := gzipcache.New()
if err != nil {
	return err
}
path, handler := examplev1connect.NewWatchServiceHandler(svc, cache.WithCompression())
```

`WithCompression` replaces connect's `gzip` on that handler only. Clients do
not change: any client that accepts gzip, over the Connect or gRPC protocol,
receives standard gzip frames.

Options:

| Option | Default | |
|---|---|---|
| `WithLevel` | `gzip.DefaultCompression` | Each payload is compressed once, so a higher level is cheap. |
| `WithMinBytes` | 64 KiB | Smaller payloads are compressed per send and never cached. |
| `WithMaxEntries` | 4 | Distinct payloads held at once. |
| `WithMaxBytes` | 64 MiB | Input plus compressed bytes held. Larger payloads are compressed per send. |

`Cache.Stats` reports hits, waits, misses, bypasses, fallbacks, evictions and
the bytes held, for export to your metrics system.

### Correctness

- A cache hit is verified byte for byte against the stored input, so a hash
  collision never serves the wrong payload.
- The cache keeps its own copy of the payload; connect is free to reuse its
  buffer.
- connect drives a compressor as `Reset(dst)`, one `Write` with the whole
  message, `Close`. Any other sequence (several writes, a reset mid-send)
  falls back to plain gzip for that send.
- A failed or panicking compression releases every waiting send, which then
  compresses on its own.

The cache does not help unless the same payload bytes are sent repeatedly.
Build the broadcast message once per update, not once per stream.

### Benchmark

`BenchmarkFanout`: 52 sends of a fresh 2.2 MB Cedar-like payload per
iteration, AMD Ryzen 7 PRO 8840HS.

| | stock gzip | gzipcache |
|---|---|---|
| CPU per publish, 1 core (`-cpu 1`) | 669 ms | 23.5 ms |
| Wall per publish, 16 cores | 114 ms | 15.9 ms |
| Allocated per publish | 32.2 MiB | 11.4 MiB |

### Limits

connect-go copies the marshaled message into a pooled buffer on every send,
and nothing outside connect can skip that. gzipcache removes the compression
cost, not the per-send marshal buffer.

## gzipcache

A gzip compressor for [connect-go](https://github.com/connectrpc/connect-go)
that compresses each distinct payload once, for servers that send the same
message on many streams.

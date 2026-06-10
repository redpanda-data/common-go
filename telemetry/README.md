# telemetry

Shared, anonymous telemetry client for Redpanda products (Connect, Console, the
Kubernetes operator). Provides a transport `Client` that signs a payload as an
RS256 JWT and POSTs it, plus a `Reporter` that runs a periodic collect→send loop
with drop-on-error semantics. Each product supplies its own signing key, endpoint
path, and payload.

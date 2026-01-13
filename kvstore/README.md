# KV Store

Kafka-backed key-value store with read-your-own-writes semantics. Uses Kafka as write-ahead log, go-memdb for local reads. Single partition, eventually consistent across replicas, your writes block until visible.

## Architecture

```
                            Kafka Topic (single partition)
                           +-----------------------------+
                           |  [0] [1] [2] [3] [4] ...    |
                           +-----------------------------+
                                 ^              |
                                 |              | consume
                          produce|              v
    +------------------------+   |    +------------------+
    |        Client          |   |    |  Consumer Loop   |
    |                        |---+    |                  |
    |  Put(k,v) ------------>|        |  for record :=   |
    |    |                   |        |    storage.Set() |
    |    | blocks until      |        |    notify()      |
    |    v offset consumed   |        +--------+---------+
    |                        |                 |
    |  Get(k) --+            |                 v
    |           |            |        +------------------+
    +-----------+------------+        |  Local Storage   |
                |                     |    (go-memdb)    |
                +-------------------->|                  |
                      direct read     +------------------+
```

**Write path:** `Put()` produces to Kafka, blocks until consumer processes that offset.

**Read path:** `Get()` reads directly from local memdb. No network round-trip.

**Consistency:** Read-your-own-writes guaranteed. Cross-client eventually consistent.

## Why

You need a simple KV store but don't want to run another database. You already have Kafka. This uses a single-partition topic as the source of truth with local in-memory reads.

Each client produces to Kafka and consumes into local memory. Writes block until your consumer catches up, so you read what you write. Other clients eventually see it when their consumers catch up.

Not linearizable. Not for strong consistency.

## Basic Usage

```go
storage, _ := memdb.New()
client, _ := kv.NewClient(ctx, "my-topic", storage,
    kv.WithBrokers("localhost:9092"),
)
defer client.Close()

// Writes block until visible in reads
client.Put(ctx, []byte("key"), []byte("value"))

// Read immediately works - already visible
value, _ := client.Get(ctx, []byte("key"))

// Delete also blocks
client.Delete(ctx, []byte("key"))
```

## Typed Client with JSON

```go
type User struct {
    Email string `json:"email"`
    Name  string `json:"name"`
}

storage, _ := memdb.New()
client, _ := kv.NewClient(ctx, "users", storage,
    kv.WithBrokers("localhost:9092"),
)

users := kv.NewResourceClient[User](client, kv.JSON[User]())

// Put/Get with structs
users.Put(ctx, []byte("user:1"), User{Email: "alice@example.com", Name: "Alice"})
user, _ := users.Get(ctx, []byte("user:1"))

// Range queries by prefix
for item, err := range users.Range(ctx, kv.Prefix("user:"), kv.QueryOptions{Limit: 100}) {
    if err != nil {
        break
    }
    fmt.Printf("%s: %s\n", item.Key, item.Value.Name)
}
```

## Typed Client with Protobuf

```go
import pb "example.com/proto/users"

storage, _ := memdb.New()
client, _ := kv.NewClient(ctx, "accounts", storage,
    kv.WithBrokers("localhost:9092"),
)

serde, _ := kv.Proto(func() *pb.Account {
    return &pb.Account{}
})
accounts := kv.NewResourceClient[*pb.Account](client, serde)

accounts.Put(ctx, []byte("acct:1"), &pb.Account{Name: "Acme Corp"})
account, _ := accounts.Get(ctx, []byte("acct:1"))
```

## Batch Operations

Batch writes sync once on the highest offset. Much faster than individual Puts.

```go
// Individual puts: ~15ms each
client.Put(ctx, []byte("key1"), value1)
client.Put(ctx, []byte("key2"), value2)

// Batch: ~0.16ms per op for 100 items
client.Batch().
    Put([]byte("key1"), value1).
    Put([]byte("key2"), value2).
    Put([]byte("key3"), value3).
    Delete([]byte("old-key")).
    Execute(ctx)
```

## Range Queries

Prefix scans or arbitrary ranges.

```go
// Prefix scan
for kv, err := range client.Range(ctx, kv.Prefix("user:"), kv.QueryOptions{Limit: 100}) {
    // Returns user:1, user:2, etc
}

// Arbitrary range (start inclusive, end exclusive)
constraint := kv.KeyConstraint{Start: []byte("a"), End: []byte("z")}
for kv, err := range client.Range(ctx, constraint, kv.QueryOptions{}) {
    // All keys between a and z
}
```

## Constraints

- Single partition only (enforced at topic creation)
- Entire dataset must fit in memory
- Write throughput limited by single partition (~10K ops/sec)
- No disk persistence - rebuilt from Kafka on restart
- No unique constraints enforced
- No automatic eviction or TTL

## Testing

```bash
# Full tests (needs Docker for testcontainers)
go test -v ./...

# Unit tests only
go test -short -v ./...

# With race detector
go test -race -v ./...
```

## Configuration

One topic per resource type. Create separate clients for different types.

```go
// Users
usersStorage, _ := memdb.New()
users, _ := kv.NewClient(ctx, "users-topic", usersStorage,
    kv.WithBrokers("localhost:9092"),
    kv.WithReplicationFactor(3), // optional
)

// Products
productsStorage, _ := memdb.New()
products, _ := kv.NewClient(ctx, "products-topic", productsStorage,
    kv.WithBrokers("localhost:9092"),
)
```


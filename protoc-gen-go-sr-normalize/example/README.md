# protoc-gen-go-sr example

Demonstrates generating Schema Registry schemas from annotated proto messages.

## Generate

```bash
cd example
buf generate
```

This produces:

```
gen/go/example/v1/
  product.pb.go        # Standard protobuf code (from protoc-gen-go)
  product_sr.go        # Schema Registry constants (from protoc-gen-go-sr)
```

## What gets generated

`product_sr.go` contains:

```go
const ProductSRSchema = `...self-contained proto with Product at index 0...`
const ProductSRSubject = "example.products-value"
```

The schema string is a valid `.proto` file containing only `Product`, its
dependency `ProductStatus`, and the `google/protobuf/timestamp.proto` import.
Services, request/response wrappers, and annotations are stripped.

## Using with kvstore

```go
serde, err := kvstore.Proto(
    func() *examplev1.Product { return &examplev1.Product{} },
    kvstore.WithSchemaRegistry(srClient, examplev1.ProductSRSubject, examplev1.ProductSRSchema),
)
```

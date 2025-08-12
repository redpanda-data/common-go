module github.com/redpanda-data/common-go/proto

go 1.23.5

toolchain go1.24.2

require (
	buf.build/gen/go/redpandadata/cloud/protocolbuffers/go v1.36.6-20250320090119-84779f9e5085.1
	buf.build/gen/go/redpandadata/dataplane/protocolbuffers/go v1.36.1-20250404200318-65f29ddd7b29.1
	connectrpc.com/connect v1.18.1
	github.com/mark3labs/mcp-go v0.20.0
	github.com/redpanda-data/protoc-gen-go-mcp v0.0.0-20250812150403-af36b599bd0e
	google.golang.org/genproto/googleapis/api v0.0.0-20250409194420-de1ac958c67a
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250409194420-de1ac958c67a
	google.golang.org/grpc v1.71.0
	google.golang.org/protobuf v1.36.6
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.6-20250307204501-0409229c3780.1 // indirect
	buf.build/gen/go/grpc-ecosystem/grpc-gateway/protocolbuffers/go v1.36.6-20221127060915-a1ecdc58eccd.1 // indirect
	buf.build/gen/go/redpandadata/common/protocolbuffers/go v1.36.6-20240917150400-3f349e63f44a.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/redpanda-data/common-go/api v0.0.0-20250801174835-9eea07f1ea06 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/net v0.37.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto v0.0.0-20250409194420-de1ac958c67a // indirect
)

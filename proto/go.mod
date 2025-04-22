module github.com/redpanda-data/common-go/proto

go 1.23.0

toolchain go1.24.2

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.6-20250307204501-0409229c3780.1
	buf.build/gen/go/redpandadata/cloud/protocolbuffers/go v1.36.6-20250320090119-84779f9e5085.1
	buf.build/gen/go/redpandadata/common/protocolbuffers/go v1.36.6-20240917150400-3f349e63f44a.1
	buf.build/gen/go/redpandadata/dataplane/protocolbuffers/go v1.36.1-20250404200318-65f29ddd7b29.1
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.26.3
	github.com/mark3labs/mcp-go v0.20.0
	google.golang.org/genproto v0.0.0-20250409194420-de1ac958c67a
	google.golang.org/genproto/googleapis/api v0.0.0-20250409194420-de1ac958c67a
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250409194420-de1ac958c67a
	google.golang.org/protobuf v1.36.6
)

require (
	buf.build/gen/go/grpc-ecosystem/grpc-gateway/protocolbuffers/go v1.36.6-20221127060915-a1ecdc58eccd.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
)

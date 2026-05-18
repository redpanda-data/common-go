module github.com/redpanda-data/common-go/authz/grpcauthz

go 1.25.10

replace github.com/redpanda-data/common-go/authz => ../

require (
	buf.build/gen/go/redpandadata/common/protocolbuffers/go v1.36.11-20260323171043-6e06f84ad823.1
	github.com/redpanda-data/common-go/authz v0.3.0
	go.opentelemetry.io/otel/trace v1.43.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260511170946-3700d4141b60
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.43.0 // indirect
	go.opentelemetry.io/otel/metric v1.43.0 // indirect
	golang.org/x/net v0.54.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260511170946-3700d4141b60 // indirect
)

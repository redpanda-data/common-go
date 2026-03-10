module github.com/redpanda-data/common-go/authz/connectinterceptor

go 1.25.8

replace github.com/redpanda-data/common-go/authz => ../

require (
	connectrpc.com/connect v1.19.1
	github.com/redpanda-data/common-go/authz v0.0.0-00010101000000-000000000000
	go.uber.org/zap v1.27.1
)

require (
	buf.build/gen/go/redpandadata/common/protocolbuffers/go v1.36.11-20260310135854-8efbf3a00d6e.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.42.0 // indirect
	go.opentelemetry.io/otel/metric v1.42.0 // indirect
	go.opentelemetry.io/otel/trace v1.42.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/net v0.38.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.23.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.71.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

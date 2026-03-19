module github.com/redpanda-data/common-go/authz/grpcauthz

go 1.25.8

replace github.com/redpanda-data/common-go/authz => ../

require (
	buf.build/gen/go/redpandadata/common/protocolbuffers/go v1.36.11-20260316210807-5d899910f714.1
	github.com/redpanda-data/common-go/authz v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otel/trace v1.42.0
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260319171110-e3a33c96fb44
	google.golang.org/grpc v1.79.3
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.42.0 // indirect
	go.opentelemetry.io/otel/metric v1.42.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260226221140-a57be14db171 // indirect
)

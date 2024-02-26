package errors

import (
	commonv1alpha1 "buf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1alpha1"
	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/code"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

// StatusToNice converts a google.rpc.Status to commonv1alpha1.ErrorStatus,
// which is "nicer" variant with Code as Enum.
func StatusToNice(s *spb.Status) *commonv1alpha1.ErrorStatus {
	return &commonv1alpha1.ErrorStatus{
		Code:    code.Code(s.Code),
		Message: s.Message,
		Details: s.Details,
	}
}

// ConnectErrorToGoogleStatus converts a connect.Error into the gRPC compliant
// spb.Status type that can be used to present errors.
func ConnectErrorToGoogleStatus(connectErr *connect.Error) *spb.Status {
	st := &spb.Status{
		Code:    int32(connectErr.Code()),
		Message: connectErr.Message(),
		Details: nil, // Will be set in the next for loop
	}
	for _, detail := range connectErr.Details() {
		anyDetail := &anypb.Any{
			TypeUrl: detail.Type(),
			Value:   detail.Bytes(),
		}
		st.Details = append(st.Details, anyDetail)
	}

	return st
}

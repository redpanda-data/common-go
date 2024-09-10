// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: redpanda/api/common/v1alpha1/common.proto

package commonv1alpha1

import (
	code "google.golang.org/genproto/googleapis/rpc/code"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	anypb "google.golang.org/protobuf/types/known/anypb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Reason int32

const (
	Reason_REASON_UNSPECIFIED Reason = 0
	// The specified resource could not be found.
	Reason_REASON_RESOURCE_NOT_FOUND Reason = 1
	// The input provided with the request is invalid.
	Reason_REASON_INVALID_INPUT Reason = 2
	// Authentication token is missing.
	Reason_REASON_NO_AUTHENTICATION_TOKEN Reason = 3
	// The authentication token provided has expired.
	Reason_REASON_AUTHENTICATION_TOKEN_EXPIRED Reason = 4
	// The authentication token provided is invalid.
	Reason_REASON_AUTHENTICATION_TOKEN_INVALID Reason = 5
	// The user does not have the necessary permissions.
	Reason_REASON_PERMISSION_DENIED Reason = 6
	// The request cannot be completed due to server error.
	Reason_REASON_SERVER_ERROR Reason = 7
	// The request rate is too high.
	Reason_REASON_TOO_MANY_REQUESTS Reason = 8
	// The request timed out.
	Reason_REASON_TIMEOUT Reason = 9
	// The feature is not configured.
	Reason_REASON_FEATURE_NOT_CONFIGURED Reason = 10
	// The feature is not supported in the requested environment.
	Reason_REASON_FEATURE_NOT_SUPPORTED Reason = 11
)

// Enum value maps for Reason.
var (
	Reason_name = map[int32]string{
		0:  "REASON_UNSPECIFIED",
		1:  "REASON_RESOURCE_NOT_FOUND",
		2:  "REASON_INVALID_INPUT",
		3:  "REASON_NO_AUTHENTICATION_TOKEN",
		4:  "REASON_AUTHENTICATION_TOKEN_EXPIRED",
		5:  "REASON_AUTHENTICATION_TOKEN_INVALID",
		6:  "REASON_PERMISSION_DENIED",
		7:  "REASON_SERVER_ERROR",
		8:  "REASON_TOO_MANY_REQUESTS",
		9:  "REASON_TIMEOUT",
		10: "REASON_FEATURE_NOT_CONFIGURED",
		11: "REASON_FEATURE_NOT_SUPPORTED",
	}
	Reason_value = map[string]int32{
		"REASON_UNSPECIFIED":                  0,
		"REASON_RESOURCE_NOT_FOUND":           1,
		"REASON_INVALID_INPUT":                2,
		"REASON_NO_AUTHENTICATION_TOKEN":      3,
		"REASON_AUTHENTICATION_TOKEN_EXPIRED": 4,
		"REASON_AUTHENTICATION_TOKEN_INVALID": 5,
		"REASON_PERMISSION_DENIED":            6,
		"REASON_SERVER_ERROR":                 7,
		"REASON_TOO_MANY_REQUESTS":            8,
		"REASON_TIMEOUT":                      9,
		"REASON_FEATURE_NOT_CONFIGURED":       10,
		"REASON_FEATURE_NOT_SUPPORTED":        11,
	}
)

func (x Reason) Enum() *Reason {
	p := new(Reason)
	*p = x
	return p
}

func (x Reason) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (Reason) Descriptor() protoreflect.EnumDescriptor {
	return file_redpanda_api_common_v1alpha1_common_proto_enumTypes[0].Descriptor()
}

func (Reason) Type() protoreflect.EnumType {
	return &file_redpanda_api_common_v1alpha1_common_proto_enumTypes[0]
}

func (x Reason) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use Reason.Descriptor instead.
func (Reason) EnumDescriptor() ([]byte, []int) {
	return file_redpanda_api_common_v1alpha1_common_proto_rawDescGZIP(), []int{0}
}

// Modified variant of google.rpc.Status, that uses enum instead of int32 for
// code, so it's nicer in REST.
// The `Status` type defines a logical error model that is suitable for
// different programming environments, including REST APIs and RPC APIs. It is
// used by [gRPC](https://github.com/grpc). Each `Status` message contains
// three pieces of data: error code, error message, and error details.
//
// You can find out more about this error model and how to work with it in the
// [API Design Guide](https://cloud.google.com/apis/design/errors).
type ErrorStatus struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// The status code, which should be an enum value of
	// [google.rpc.Code][google.rpc.Code].
	Code code.Code `protobuf:"varint,1,opt,name=code,proto3,enum=google.rpc.Code" json:"code,omitempty"`
	// A developer-facing error message, which should be in English. Any
	// user-facing error message should be localized and sent in the
	// [google.rpc.Status.details][google.rpc.Status.details] field, or localized
	// by the client.
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
	// A list of messages that carry the error details.  There is a common set of
	// message types for APIs to use.
	Details []*anypb.Any `protobuf:"bytes,3,rep,name=details,proto3" json:"details,omitempty"`
}

func (x *ErrorStatus) Reset() {
	*x = ErrorStatus{}
	if protoimpl.UnsafeEnabled {
		mi := &file_redpanda_api_common_v1alpha1_common_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ErrorStatus) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ErrorStatus) ProtoMessage() {}

func (x *ErrorStatus) ProtoReflect() protoreflect.Message {
	mi := &file_redpanda_api_common_v1alpha1_common_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ErrorStatus.ProtoReflect.Descriptor instead.
func (*ErrorStatus) Descriptor() ([]byte, []int) {
	return file_redpanda_api_common_v1alpha1_common_proto_rawDescGZIP(), []int{0}
}

func (x *ErrorStatus) GetCode() code.Code {
	if x != nil {
		return x.Code
	}
	return code.Code(0)
}

func (x *ErrorStatus) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

func (x *ErrorStatus) GetDetails() []*anypb.Any {
	if x != nil {
		return x.Details
	}
	return nil
}

var File_redpanda_api_common_v1alpha1_common_proto protoreflect.FileDescriptor

var file_redpanda_api_common_v1alpha1_common_proto_rawDesc = []byte{
	0x0a, 0x29, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x63,
	0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x76, 0x31, 0x61, 0x6c, 0x70, 0x68, 0x61, 0x31, 0x2f, 0x63,
	0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x1c, 0x72, 0x65, 0x64,
	0x70, 0x61, 0x6e, 0x64, 0x61, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e,
	0x2e, 0x76, 0x31, 0x61, 0x6c, 0x70, 0x68, 0x61, 0x31, 0x1a, 0x19, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x61, 0x6e, 0x79, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x15, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x72, 0x70, 0x63,
	0x2f, 0x63, 0x6f, 0x64, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x7d, 0x0a, 0x0b, 0x45,
	0x72, 0x72, 0x6f, 0x72, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x24, 0x0a, 0x04, 0x63, 0x6f,
	0x64, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2e, 0x72, 0x70, 0x63, 0x2e, 0x43, 0x6f, 0x64, 0x65, 0x52, 0x04, 0x63, 0x6f, 0x64, 0x65,
	0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x12, 0x2e, 0x0a, 0x07, 0x64, 0x65,
	0x74, 0x61, 0x69, 0x6c, 0x73, 0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e,
	0x79, 0x52, 0x07, 0x64, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x2a, 0xfd, 0x02, 0x0a, 0x06, 0x52,
	0x65, 0x61, 0x73, 0x6f, 0x6e, 0x12, 0x16, 0x0a, 0x12, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f,
	0x55, 0x4e, 0x53, 0x50, 0x45, 0x43, 0x49, 0x46, 0x49, 0x45, 0x44, 0x10, 0x00, 0x12, 0x1d, 0x0a,
	0x19, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x52, 0x45, 0x53, 0x4f, 0x55, 0x52, 0x43, 0x45,
	0x5f, 0x4e, 0x4f, 0x54, 0x5f, 0x46, 0x4f, 0x55, 0x4e, 0x44, 0x10, 0x01, 0x12, 0x18, 0x0a, 0x14,
	0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x49, 0x4e, 0x56, 0x41, 0x4c, 0x49, 0x44, 0x5f, 0x49,
	0x4e, 0x50, 0x55, 0x54, 0x10, 0x02, 0x12, 0x22, 0x0a, 0x1e, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e,
	0x5f, 0x4e, 0x4f, 0x5f, 0x41, 0x55, 0x54, 0x48, 0x45, 0x4e, 0x54, 0x49, 0x43, 0x41, 0x54, 0x49,
	0x4f, 0x4e, 0x5f, 0x54, 0x4f, 0x4b, 0x45, 0x4e, 0x10, 0x03, 0x12, 0x27, 0x0a, 0x23, 0x52, 0x45,
	0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x41, 0x55, 0x54, 0x48, 0x45, 0x4e, 0x54, 0x49, 0x43, 0x41, 0x54,
	0x49, 0x4f, 0x4e, 0x5f, 0x54, 0x4f, 0x4b, 0x45, 0x4e, 0x5f, 0x45, 0x58, 0x50, 0x49, 0x52, 0x45,
	0x44, 0x10, 0x04, 0x12, 0x27, 0x0a, 0x23, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x41, 0x55,
	0x54, 0x48, 0x45, 0x4e, 0x54, 0x49, 0x43, 0x41, 0x54, 0x49, 0x4f, 0x4e, 0x5f, 0x54, 0x4f, 0x4b,
	0x45, 0x4e, 0x5f, 0x49, 0x4e, 0x56, 0x41, 0x4c, 0x49, 0x44, 0x10, 0x05, 0x12, 0x1c, 0x0a, 0x18,
	0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x50, 0x45, 0x52, 0x4d, 0x49, 0x53, 0x53, 0x49, 0x4f,
	0x4e, 0x5f, 0x44, 0x45, 0x4e, 0x49, 0x45, 0x44, 0x10, 0x06, 0x12, 0x17, 0x0a, 0x13, 0x52, 0x45,
	0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x53, 0x45, 0x52, 0x56, 0x45, 0x52, 0x5f, 0x45, 0x52, 0x52, 0x4f,
	0x52, 0x10, 0x07, 0x12, 0x1c, 0x0a, 0x18, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x54, 0x4f,
	0x4f, 0x5f, 0x4d, 0x41, 0x4e, 0x59, 0x5f, 0x52, 0x45, 0x51, 0x55, 0x45, 0x53, 0x54, 0x53, 0x10,
	0x08, 0x12, 0x12, 0x0a, 0x0e, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f, 0x54, 0x49, 0x4d, 0x45,
	0x4f, 0x55, 0x54, 0x10, 0x09, 0x12, 0x21, 0x0a, 0x1d, 0x52, 0x45, 0x41, 0x53, 0x4f, 0x4e, 0x5f,
	0x46, 0x45, 0x41, 0x54, 0x55, 0x52, 0x45, 0x5f, 0x4e, 0x4f, 0x54, 0x5f, 0x43, 0x4f, 0x4e, 0x46,
	0x49, 0x47, 0x55, 0x52, 0x45, 0x44, 0x10, 0x0a, 0x12, 0x20, 0x0a, 0x1c, 0x52, 0x45, 0x41, 0x53,
	0x4f, 0x4e, 0x5f, 0x46, 0x45, 0x41, 0x54, 0x55, 0x52, 0x45, 0x5f, 0x4e, 0x4f, 0x54, 0x5f, 0x53,
	0x55, 0x50, 0x50, 0x4f, 0x52, 0x54, 0x45, 0x44, 0x10, 0x0b, 0x42, 0xa7, 0x02, 0x0a, 0x20, 0x63,
	0x6f, 0x6d, 0x2e, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x2e, 0x61, 0x70, 0x69, 0x2e,
	0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x76, 0x31, 0x61, 0x6c, 0x70, 0x68, 0x61, 0x31, 0x42,
	0x0b, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x63,
	0x62, 0x75, 0x66, 0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x67, 0x6f,
	0x2f, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x64, 0x61, 0x74, 0x61, 0x2f, 0x63, 0x6f,
	0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x62, 0x75, 0x66,
	0x66, 0x65, 0x72, 0x73, 0x2f, 0x67, 0x6f, 0x2f, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61,
	0x2f, 0x61, 0x70, 0x69, 0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x76, 0x31, 0x61, 0x6c,
	0x70, 0x68, 0x61, 0x31, 0x3b, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x76, 0x31, 0x61, 0x6c, 0x70,
	0x68, 0x61, 0x31, 0xa2, 0x02, 0x03, 0x52, 0x41, 0x43, 0xaa, 0x02, 0x1c, 0x52, 0x65, 0x64, 0x70,
	0x61, 0x6e, 0x64, 0x61, 0x2e, 0x41, 0x70, 0x69, 0x2e, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e,
	0x56, 0x31, 0x61, 0x6c, 0x70, 0x68, 0x61, 0x31, 0xca, 0x02, 0x1c, 0x52, 0x65, 0x64, 0x70, 0x61,
	0x6e, 0x64, 0x61, 0x5c, 0x41, 0x70, 0x69, 0x5c, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x5c, 0x56,
	0x31, 0x61, 0x6c, 0x70, 0x68, 0x61, 0x31, 0xe2, 0x02, 0x28, 0x52, 0x65, 0x64, 0x70, 0x61, 0x6e,
	0x64, 0x61, 0x5c, 0x41, 0x70, 0x69, 0x5c, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x5c, 0x56, 0x31,
	0x61, 0x6c, 0x70, 0x68, 0x61, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61,
	0x74, 0x61, 0xea, 0x02, 0x1f, 0x52, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x3a, 0x3a, 0x41,
	0x70, 0x69, 0x3a, 0x3a, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x3a, 0x3a, 0x56, 0x31, 0x61, 0x6c,
	0x70, 0x68, 0x61, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_redpanda_api_common_v1alpha1_common_proto_rawDescOnce sync.Once
	file_redpanda_api_common_v1alpha1_common_proto_rawDescData = file_redpanda_api_common_v1alpha1_common_proto_rawDesc
)

func file_redpanda_api_common_v1alpha1_common_proto_rawDescGZIP() []byte {
	file_redpanda_api_common_v1alpha1_common_proto_rawDescOnce.Do(func() {
		file_redpanda_api_common_v1alpha1_common_proto_rawDescData = protoimpl.X.CompressGZIP(file_redpanda_api_common_v1alpha1_common_proto_rawDescData)
	})
	return file_redpanda_api_common_v1alpha1_common_proto_rawDescData
}

var file_redpanda_api_common_v1alpha1_common_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_redpanda_api_common_v1alpha1_common_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_redpanda_api_common_v1alpha1_common_proto_goTypes = []any{
	(Reason)(0),         // 0: redpanda.api.common.v1alpha1.Reason
	(*ErrorStatus)(nil), // 1: redpanda.api.common.v1alpha1.ErrorStatus
	(code.Code)(0),      // 2: google.rpc.Code
	(*anypb.Any)(nil),   // 3: google.protobuf.Any
}
var file_redpanda_api_common_v1alpha1_common_proto_depIdxs = []int32{
	2, // 0: redpanda.api.common.v1alpha1.ErrorStatus.code:type_name -> google.rpc.Code
	3, // 1: redpanda.api.common.v1alpha1.ErrorStatus.details:type_name -> google.protobuf.Any
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_redpanda_api_common_v1alpha1_common_proto_init() }
func file_redpanda_api_common_v1alpha1_common_proto_init() {
	if File_redpanda_api_common_v1alpha1_common_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_redpanda_api_common_v1alpha1_common_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*ErrorStatus); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_redpanda_api_common_v1alpha1_common_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_redpanda_api_common_v1alpha1_common_proto_goTypes,
		DependencyIndexes: file_redpanda_api_common_v1alpha1_common_proto_depIdxs,
		EnumInfos:         file_redpanda_api_common_v1alpha1_common_proto_enumTypes,
		MessageInfos:      file_redpanda_api_common_v1alpha1_common_proto_msgTypes,
	}.Build()
	File_redpanda_api_common_v1alpha1_common_proto = out.File
	file_redpanda_api_common_v1alpha1_common_proto_rawDesc = nil
	file_redpanda_api_common_v1alpha1_common_proto_goTypes = nil
	file_redpanda_api_common_v1alpha1_common_proto_depIdxs = nil
}

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.34.2
// 	protoc        (unknown)
// source: redpanda/api/common/v1/errordetails.proto

package commonv1

import (
	status "google.golang.org/genproto/googleapis/rpc/status"
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

// AttemptInfo contains information about retryable actions and their specific attempts.
type AttemptInfo struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Attempts []*AttemptInfo_Attempt `protobuf:"bytes,1,rep,name=attempts,proto3" json:"attempts,omitempty"`
}

func (x *AttemptInfo) Reset() {
	*x = AttemptInfo{}
	if protoimpl.UnsafeEnabled {
		mi := &file_redpanda_api_common_v1_errordetails_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AttemptInfo) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AttemptInfo) ProtoMessage() {}

func (x *AttemptInfo) ProtoReflect() protoreflect.Message {
	mi := &file_redpanda_api_common_v1_errordetails_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AttemptInfo.ProtoReflect.Descriptor instead.
func (*AttemptInfo) Descriptor() ([]byte, []int) {
	return file_redpanda_api_common_v1_errordetails_proto_rawDescGZIP(), []int{0}
}

func (x *AttemptInfo) GetAttempts() []*AttemptInfo_Attempt {
	if x != nil {
		return x.Attempts
	}
	return nil
}

// ExternalError is an error that may be returned to external users. Other
// errors thrown by internal systems are discarded by default, so internal
// errors with sensitive information are not exposed.
type ExternalError struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Message string       `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
	Details []*anypb.Any `protobuf:"bytes,2,rep,name=details,proto3" json:"details,omitempty"`
}

func (x *ExternalError) Reset() {
	*x = ExternalError{}
	if protoimpl.UnsafeEnabled {
		mi := &file_redpanda_api_common_v1_errordetails_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ExternalError) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ExternalError) ProtoMessage() {}

func (x *ExternalError) ProtoReflect() protoreflect.Message {
	mi := &file_redpanda_api_common_v1_errordetails_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ExternalError.ProtoReflect.Descriptor instead.
func (*ExternalError) Descriptor() ([]byte, []int) {
	return file_redpanda_api_common_v1_errordetails_proto_rawDescGZIP(), []int{1}
}

func (x *ExternalError) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

func (x *ExternalError) GetDetails() []*anypb.Any {
	if x != nil {
		return x.Details
	}
	return nil
}

type AttemptInfo_Attempt struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Number int32          `protobuf:"varint,1,opt,name=number,proto3" json:"number,omitempty"`
	Status *status.Status `protobuf:"bytes,2,opt,name=status,proto3" json:"status,omitempty"`
}

func (x *AttemptInfo_Attempt) Reset() {
	*x = AttemptInfo_Attempt{}
	if protoimpl.UnsafeEnabled {
		mi := &file_redpanda_api_common_v1_errordetails_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *AttemptInfo_Attempt) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*AttemptInfo_Attempt) ProtoMessage() {}

func (x *AttemptInfo_Attempt) ProtoReflect() protoreflect.Message {
	mi := &file_redpanda_api_common_v1_errordetails_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use AttemptInfo_Attempt.ProtoReflect.Descriptor instead.
func (*AttemptInfo_Attempt) Descriptor() ([]byte, []int) {
	return file_redpanda_api_common_v1_errordetails_proto_rawDescGZIP(), []int{0, 0}
}

func (x *AttemptInfo_Attempt) GetNumber() int32 {
	if x != nil {
		return x.Number
	}
	return 0
}

func (x *AttemptInfo_Attempt) GetStatus() *status.Status {
	if x != nil {
		return x.Status
	}
	return nil
}

var File_redpanda_api_common_v1_errordetails_proto protoreflect.FileDescriptor

var file_redpanda_api_common_v1_errordetails_proto_rawDesc = []byte{
	0x0a, 0x29, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x2f, 0x61, 0x70, 0x69, 0x2f, 0x63,
	0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x76, 0x31, 0x2f, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x64, 0x65,
	0x74, 0x61, 0x69, 0x6c, 0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x16, 0x72, 0x65, 0x64,
	0x70, 0x61, 0x6e, 0x64, 0x61, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e,
	0x2e, 0x76, 0x31, 0x1a, 0x19, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x62, 0x75, 0x66, 0x2f, 0x61, 0x6e, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x17,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x72, 0x70, 0x63, 0x2f, 0x73, 0x74, 0x61, 0x74, 0x75,
	0x73, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xa5, 0x01, 0x0a, 0x0b, 0x41, 0x74, 0x74, 0x65,
	0x6d, 0x70, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x12, 0x47, 0x0a, 0x08, 0x61, 0x74, 0x74, 0x65, 0x6d,
	0x70, 0x74, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x2b, 0x2e, 0x72, 0x65, 0x64, 0x70,
	0x61, 0x6e, 0x64, 0x61, 0x2e, 0x61, 0x70, 0x69, 0x2e, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e,
	0x76, 0x31, 0x2e, 0x41, 0x74, 0x74, 0x65, 0x6d, 0x70, 0x74, 0x49, 0x6e, 0x66, 0x6f, 0x2e, 0x41,
	0x74, 0x74, 0x65, 0x6d, 0x70, 0x74, 0x52, 0x08, 0x61, 0x74, 0x74, 0x65, 0x6d, 0x70, 0x74, 0x73,
	0x1a, 0x4d, 0x0a, 0x07, 0x41, 0x74, 0x74, 0x65, 0x6d, 0x70, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x6e,
	0x75, 0x6d, 0x62, 0x65, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x05, 0x52, 0x06, 0x6e, 0x75, 0x6d,
	0x62, 0x65, 0x72, 0x12, 0x2a, 0x0a, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x72, 0x70, 0x63,
	0x2e, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x22,
	0x59, 0x0a, 0x0d, 0x45, 0x78, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x45, 0x72, 0x72, 0x6f, 0x72,
	0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x12, 0x2e, 0x0a, 0x07, 0x64, 0x65,
	0x74, 0x61, 0x69, 0x6c, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e,
	0x79, 0x52, 0x07, 0x64, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x42, 0x83, 0x02, 0x0a, 0x1a, 0x63,
	0x6f, 0x6d, 0x2e, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x2e, 0x61, 0x70, 0x69, 0x2e,
	0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2e, 0x76, 0x31, 0x42, 0x11, 0x45, 0x72, 0x72, 0x6f, 0x72,
	0x64, 0x65, 0x74, 0x61, 0x69, 0x6c, 0x73, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01, 0x5a, 0x57,
	0x62, 0x75, 0x66, 0x2e, 0x62, 0x75, 0x69, 0x6c, 0x64, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x67, 0x6f,
	0x2f, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x64, 0x61, 0x74, 0x61, 0x2f, 0x63, 0x6f,
	0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x63, 0x6f, 0x6c, 0x62, 0x75, 0x66,
	0x66, 0x65, 0x72, 0x73, 0x2f, 0x67, 0x6f, 0x2f, 0x72, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61,
	0x2f, 0x61, 0x70, 0x69, 0x2f, 0x63, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x2f, 0x76, 0x31, 0x3b, 0x63,
	0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x76, 0x31, 0xa2, 0x02, 0x03, 0x52, 0x41, 0x43, 0xaa, 0x02, 0x16,
	0x52, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x2e, 0x41, 0x70, 0x69, 0x2e, 0x43, 0x6f, 0x6d,
	0x6d, 0x6f, 0x6e, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x16, 0x52, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64,
	0x61, 0x5c, 0x41, 0x70, 0x69, 0x5c, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x5c, 0x56, 0x31, 0xe2,
	0x02, 0x22, 0x52, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x5c, 0x41, 0x70, 0x69, 0x5c, 0x43,
	0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61,
	0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x19, 0x52, 0x65, 0x64, 0x70, 0x61, 0x6e, 0x64, 0x61, 0x3a,
	0x3a, 0x41, 0x70, 0x69, 0x3a, 0x3a, 0x43, 0x6f, 0x6d, 0x6d, 0x6f, 0x6e, 0x3a, 0x3a, 0x56, 0x31,
	0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_redpanda_api_common_v1_errordetails_proto_rawDescOnce sync.Once
	file_redpanda_api_common_v1_errordetails_proto_rawDescData = file_redpanda_api_common_v1_errordetails_proto_rawDesc
)

func file_redpanda_api_common_v1_errordetails_proto_rawDescGZIP() []byte {
	file_redpanda_api_common_v1_errordetails_proto_rawDescOnce.Do(func() {
		file_redpanda_api_common_v1_errordetails_proto_rawDescData = protoimpl.X.CompressGZIP(file_redpanda_api_common_v1_errordetails_proto_rawDescData)
	})
	return file_redpanda_api_common_v1_errordetails_proto_rawDescData
}

var file_redpanda_api_common_v1_errordetails_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_redpanda_api_common_v1_errordetails_proto_goTypes = []any{
	(*AttemptInfo)(nil),         // 0: redpanda.api.common.v1.AttemptInfo
	(*ExternalError)(nil),       // 1: redpanda.api.common.v1.ExternalError
	(*AttemptInfo_Attempt)(nil), // 2: redpanda.api.common.v1.AttemptInfo.Attempt
	(*anypb.Any)(nil),           // 3: google.protobuf.Any
	(*status.Status)(nil),       // 4: google.rpc.Status
}
var file_redpanda_api_common_v1_errordetails_proto_depIdxs = []int32{
	2, // 0: redpanda.api.common.v1.AttemptInfo.attempts:type_name -> redpanda.api.common.v1.AttemptInfo.Attempt
	3, // 1: redpanda.api.common.v1.ExternalError.details:type_name -> google.protobuf.Any
	4, // 2: redpanda.api.common.v1.AttemptInfo.Attempt.status:type_name -> google.rpc.Status
	3, // [3:3] is the sub-list for method output_type
	3, // [3:3] is the sub-list for method input_type
	3, // [3:3] is the sub-list for extension type_name
	3, // [3:3] is the sub-list for extension extendee
	0, // [0:3] is the sub-list for field type_name
}

func init() { file_redpanda_api_common_v1_errordetails_proto_init() }
func file_redpanda_api_common_v1_errordetails_proto_init() {
	if File_redpanda_api_common_v1_errordetails_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_redpanda_api_common_v1_errordetails_proto_msgTypes[0].Exporter = func(v any, i int) any {
			switch v := v.(*AttemptInfo); i {
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
		file_redpanda_api_common_v1_errordetails_proto_msgTypes[1].Exporter = func(v any, i int) any {
			switch v := v.(*ExternalError); i {
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
		file_redpanda_api_common_v1_errordetails_proto_msgTypes[2].Exporter = func(v any, i int) any {
			switch v := v.(*AttemptInfo_Attempt); i {
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
			RawDescriptor: file_redpanda_api_common_v1_errordetails_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_redpanda_api_common_v1_errordetails_proto_goTypes,
		DependencyIndexes: file_redpanda_api_common_v1_errordetails_proto_depIdxs,
		MessageInfos:      file_redpanda_api_common_v1_errordetails_proto_msgTypes,
	}.Build()
	File_redpanda_api_common_v1_errordetails_proto = out.File
	file_redpanda_api_common_v1_errordetails_proto_rawDesc = nil
	file_redpanda_api_common_v1_errordetails_proto_goTypes = nil
	file_redpanda_api_common_v1_errordetails_proto_depIdxs = nil
}
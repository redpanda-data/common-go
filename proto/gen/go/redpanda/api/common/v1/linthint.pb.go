// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.6
// 	protoc        (unknown)
// source: redpanda/api/common/v1/linthint.proto

package commonv1

import (
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"

	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// LintHint is a generic linting hint.
type LintHint struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Line number of the lint.
	Line int32 `protobuf:"varint,1,opt,name=line,proto3" json:"line,omitempty"`
	// Column number of the lint.
	Column int32 `protobuf:"varint,2,opt,name=column,proto3" json:"column,omitempty"`
	// The hint message.
	Hint string `protobuf:"bytes,3,opt,name=hint,proto3" json:"hint,omitempty"`
	// Optional lint type or enum.
	LintType      string `protobuf:"bytes,4,opt,name=lint_type,json=lintType,proto3" json:"lint_type,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *LintHint) Reset() {
	*x = LintHint{}
	mi := &file_redpanda_api_common_v1_linthint_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *LintHint) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*LintHint) ProtoMessage() {}

func (x *LintHint) ProtoReflect() protoreflect.Message {
	mi := &file_redpanda_api_common_v1_linthint_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use LintHint.ProtoReflect.Descriptor instead.
func (*LintHint) Descriptor() ([]byte, []int) {
	return file_redpanda_api_common_v1_linthint_proto_rawDescGZIP(), []int{0}
}

func (x *LintHint) GetLine() int32 {
	if x != nil {
		return x.Line
	}
	return 0
}

func (x *LintHint) GetColumn() int32 {
	if x != nil {
		return x.Column
	}
	return 0
}

func (x *LintHint) GetHint() string {
	if x != nil {
		return x.Hint
	}
	return ""
}

func (x *LintHint) GetLintType() string {
	if x != nil {
		return x.LintType
	}
	return ""
}

var File_redpanda_api_common_v1_linthint_proto protoreflect.FileDescriptor

const file_redpanda_api_common_v1_linthint_proto_rawDesc = "" +
	"\n" +
	"%redpanda/api/common/v1/linthint.proto\x12\x16redpanda.api.common.v1\"g\n" +
	"\bLintHint\x12\x12\n" +
	"\x04line\x18\x01 \x01(\x05R\x04line\x12\x16\n" +
	"\x06column\x18\x02 \x01(\x05R\x06column\x12\x12\n" +
	"\x04hint\x18\x03 \x01(\tR\x04hint\x12\x1b\n" +
	"\tlint_type\x18\x04 \x01(\tR\blintTypeB\xff\x01\n" +
	"\x1acom.redpanda.api.common.v1B\rLinthintProtoP\x01ZWbuf.build/gen/go/redpandadata/common/protocolbuffers/go/redpanda/api/common/v1;commonv1\xa2\x02\x03RAC\xaa\x02\x16Redpanda.Api.Common.V1\xca\x02\x16Redpanda\\Api\\Common\\V1\xe2\x02\"Redpanda\\Api\\Common\\V1\\GPBMetadata\xea\x02\x19Redpanda::Api::Common::V1b\x06proto3"

var (
	file_redpanda_api_common_v1_linthint_proto_rawDescOnce sync.Once
	file_redpanda_api_common_v1_linthint_proto_rawDescData []byte
)

func file_redpanda_api_common_v1_linthint_proto_rawDescGZIP() []byte {
	file_redpanda_api_common_v1_linthint_proto_rawDescOnce.Do(func() {
		file_redpanda_api_common_v1_linthint_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_redpanda_api_common_v1_linthint_proto_rawDesc), len(file_redpanda_api_common_v1_linthint_proto_rawDesc)))
	})
	return file_redpanda_api_common_v1_linthint_proto_rawDescData
}

var file_redpanda_api_common_v1_linthint_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_redpanda_api_common_v1_linthint_proto_goTypes = []any{
	(*LintHint)(nil), // 0: redpanda.api.common.v1.LintHint
}
var file_redpanda_api_common_v1_linthint_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_redpanda_api_common_v1_linthint_proto_init() }
func file_redpanda_api_common_v1_linthint_proto_init() {
	if File_redpanda_api_common_v1_linthint_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_redpanda_api_common_v1_linthint_proto_rawDesc), len(file_redpanda_api_common_v1_linthint_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_redpanda_api_common_v1_linthint_proto_goTypes,
		DependencyIndexes: file_redpanda_api_common_v1_linthint_proto_depIdxs,
		MessageInfos:      file_redpanda_api_common_v1_linthint_proto_msgTypes,
	}.Build()
	File_redpanda_api_common_v1_linthint_proto = out.File
	file_redpanda_api_common_v1_linthint_proto_goTypes = nil
	file_redpanda_api_common_v1_linthint_proto_depIdxs = nil
}

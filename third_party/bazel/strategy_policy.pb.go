// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.32.0
// 	protoc        v5.28.1
// source: src/main/protobuf/strategy_policy.proto

package protobuf

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type StrategyPolicy struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	MnemonicPolicy      *MnemonicPolicy `protobuf:"bytes,1,opt,name=mnemonic_policy,json=mnemonicPolicy" json:"mnemonic_policy,omitempty"`
	DynamicRemotePolicy *MnemonicPolicy `protobuf:"bytes,2,opt,name=dynamic_remote_policy,json=dynamicRemotePolicy" json:"dynamic_remote_policy,omitempty"`
	DynamicLocalPolicy  *MnemonicPolicy `protobuf:"bytes,3,opt,name=dynamic_local_policy,json=dynamicLocalPolicy" json:"dynamic_local_policy,omitempty"`
}

func (x *StrategyPolicy) Reset() {
	*x = StrategyPolicy{}
	if protoimpl.UnsafeEnabled {
		mi := &file_src_main_protobuf_strategy_policy_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *StrategyPolicy) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StrategyPolicy) ProtoMessage() {}

func (x *StrategyPolicy) ProtoReflect() protoreflect.Message {
	mi := &file_src_main_protobuf_strategy_policy_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StrategyPolicy.ProtoReflect.Descriptor instead.
func (*StrategyPolicy) Descriptor() ([]byte, []int) {
	return file_src_main_protobuf_strategy_policy_proto_rawDescGZIP(), []int{0}
}

func (x *StrategyPolicy) GetMnemonicPolicy() *MnemonicPolicy {
	if x != nil {
		return x.MnemonicPolicy
	}
	return nil
}

func (x *StrategyPolicy) GetDynamicRemotePolicy() *MnemonicPolicy {
	if x != nil {
		return x.DynamicRemotePolicy
	}
	return nil
}

func (x *StrategyPolicy) GetDynamicLocalPolicy() *MnemonicPolicy {
	if x != nil {
		return x.DynamicLocalPolicy
	}
	return nil
}

type MnemonicPolicy struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	DefaultAllowlist  []string                 `protobuf:"bytes,1,rep,name=default_allowlist,json=defaultAllowlist" json:"default_allowlist,omitempty"`
	StrategyAllowlist []*StrategiesForMnemonic `protobuf:"bytes,2,rep,name=strategy_allowlist,json=strategyAllowlist" json:"strategy_allowlist,omitempty"`
}

func (x *MnemonicPolicy) Reset() {
	*x = MnemonicPolicy{}
	if protoimpl.UnsafeEnabled {
		mi := &file_src_main_protobuf_strategy_policy_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *MnemonicPolicy) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MnemonicPolicy) ProtoMessage() {}

func (x *MnemonicPolicy) ProtoReflect() protoreflect.Message {
	mi := &file_src_main_protobuf_strategy_policy_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MnemonicPolicy.ProtoReflect.Descriptor instead.
func (*MnemonicPolicy) Descriptor() ([]byte, []int) {
	return file_src_main_protobuf_strategy_policy_proto_rawDescGZIP(), []int{1}
}

func (x *MnemonicPolicy) GetDefaultAllowlist() []string {
	if x != nil {
		return x.DefaultAllowlist
	}
	return nil
}

func (x *MnemonicPolicy) GetStrategyAllowlist() []*StrategiesForMnemonic {
	if x != nil {
		return x.StrategyAllowlist
	}
	return nil
}

type StrategiesForMnemonic struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Mnemonic *string  `protobuf:"bytes,1,opt,name=mnemonic" json:"mnemonic,omitempty"`
	Strategy []string `protobuf:"bytes,2,rep,name=strategy" json:"strategy,omitempty"`
}

func (x *StrategiesForMnemonic) Reset() {
	*x = StrategiesForMnemonic{}
	if protoimpl.UnsafeEnabled {
		mi := &file_src_main_protobuf_strategy_policy_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *StrategiesForMnemonic) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StrategiesForMnemonic) ProtoMessage() {}

func (x *StrategiesForMnemonic) ProtoReflect() protoreflect.Message {
	mi := &file_src_main_protobuf_strategy_policy_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StrategiesForMnemonic.ProtoReflect.Descriptor instead.
func (*StrategiesForMnemonic) Descriptor() ([]byte, []int) {
	return file_src_main_protobuf_strategy_policy_proto_rawDescGZIP(), []int{2}
}

func (x *StrategiesForMnemonic) GetMnemonic() string {
	if x != nil && x.Mnemonic != nil {
		return *x.Mnemonic
	}
	return ""
}

func (x *StrategiesForMnemonic) GetStrategy() []string {
	if x != nil {
		return x.Strategy
	}
	return nil
}

var File_src_main_protobuf_strategy_policy_proto protoreflect.FileDescriptor

var file_src_main_protobuf_strategy_policy_proto_rawDesc = []byte{
	0x0a, 0x27, 0x73, 0x72, 0x63, 0x2f, 0x6d, 0x61, 0x69, 0x6e, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x62, 0x75, 0x66, 0x2f, 0x73, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x79, 0x5f, 0x70, 0x6f, 0x6c,
	0x69, 0x63, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x15, 0x62, 0x6c, 0x61, 0x7a, 0x65,
	0x2e, 0x73, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x79, 0x5f, 0x70, 0x6f, 0x6c, 0x69, 0x63, 0x79,
	0x22, 0x94, 0x02, 0x0a, 0x0e, 0x53, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x79, 0x50, 0x6f, 0x6c,
	0x69, 0x63, 0x79, 0x12, 0x4e, 0x0a, 0x0f, 0x6d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x5f,
	0x70, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x25, 0x2e, 0x62,
	0x6c, 0x61, 0x7a, 0x65, 0x2e, 0x73, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x79, 0x5f, 0x70, 0x6f,
	0x6c, 0x69, 0x63, 0x79, 0x2e, 0x4d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x50, 0x6f, 0x6c,
	0x69, 0x63, 0x79, 0x52, 0x0e, 0x6d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x50, 0x6f, 0x6c,
	0x69, 0x63, 0x79, 0x12, 0x59, 0x0a, 0x15, 0x64, 0x79, 0x6e, 0x61, 0x6d, 0x69, 0x63, 0x5f, 0x72,
	0x65, 0x6d, 0x6f, 0x74, 0x65, 0x5f, 0x70, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x25, 0x2e, 0x62, 0x6c, 0x61, 0x7a, 0x65, 0x2e, 0x73, 0x74, 0x72, 0x61, 0x74,
	0x65, 0x67, 0x79, 0x5f, 0x70, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x2e, 0x4d, 0x6e, 0x65, 0x6d, 0x6f,
	0x6e, 0x69, 0x63, 0x50, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x52, 0x13, 0x64, 0x79, 0x6e, 0x61, 0x6d,
	0x69, 0x63, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x50, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x12, 0x57,
	0x0a, 0x14, 0x64, 0x79, 0x6e, 0x61, 0x6d, 0x69, 0x63, 0x5f, 0x6c, 0x6f, 0x63, 0x61, 0x6c, 0x5f,
	0x70, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x25, 0x2e, 0x62,
	0x6c, 0x61, 0x7a, 0x65, 0x2e, 0x73, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x79, 0x5f, 0x70, 0x6f,
	0x6c, 0x69, 0x63, 0x79, 0x2e, 0x4d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x50, 0x6f, 0x6c,
	0x69, 0x63, 0x79, 0x52, 0x12, 0x64, 0x79, 0x6e, 0x61, 0x6d, 0x69, 0x63, 0x4c, 0x6f, 0x63, 0x61,
	0x6c, 0x50, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x22, 0x9a, 0x01, 0x0a, 0x0e, 0x4d, 0x6e, 0x65, 0x6d,
	0x6f, 0x6e, 0x69, 0x63, 0x50, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x12, 0x2b, 0x0a, 0x11, 0x64, 0x65,
	0x66, 0x61, 0x75, 0x6c, 0x74, 0x5f, 0x61, 0x6c, 0x6c, 0x6f, 0x77, 0x6c, 0x69, 0x73, 0x74, 0x18,
	0x01, 0x20, 0x03, 0x28, 0x09, 0x52, 0x10, 0x64, 0x65, 0x66, 0x61, 0x75, 0x6c, 0x74, 0x41, 0x6c,
	0x6c, 0x6f, 0x77, 0x6c, 0x69, 0x73, 0x74, 0x12, 0x5b, 0x0a, 0x12, 0x73, 0x74, 0x72, 0x61, 0x74,
	0x65, 0x67, 0x79, 0x5f, 0x61, 0x6c, 0x6c, 0x6f, 0x77, 0x6c, 0x69, 0x73, 0x74, 0x18, 0x02, 0x20,
	0x03, 0x28, 0x0b, 0x32, 0x2c, 0x2e, 0x62, 0x6c, 0x61, 0x7a, 0x65, 0x2e, 0x73, 0x74, 0x72, 0x61,
	0x74, 0x65, 0x67, 0x79, 0x5f, 0x70, 0x6f, 0x6c, 0x69, 0x63, 0x79, 0x2e, 0x53, 0x74, 0x72, 0x61,
	0x74, 0x65, 0x67, 0x69, 0x65, 0x73, 0x46, 0x6f, 0x72, 0x4d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69,
	0x63, 0x52, 0x11, 0x73, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x79, 0x41, 0x6c, 0x6c, 0x6f, 0x77,
	0x6c, 0x69, 0x73, 0x74, 0x22, 0x4f, 0x0a, 0x15, 0x53, 0x74, 0x72, 0x61, 0x74, 0x65, 0x67, 0x69,
	0x65, 0x73, 0x46, 0x6f, 0x72, 0x4d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x12, 0x1a, 0x0a,
	0x08, 0x6d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x08, 0x6d, 0x6e, 0x65, 0x6d, 0x6f, 0x6e, 0x69, 0x63, 0x12, 0x1a, 0x0a, 0x08, 0x73, 0x74, 0x72,
	0x61, 0x74, 0x65, 0x67, 0x79, 0x18, 0x02, 0x20, 0x03, 0x28, 0x09, 0x52, 0x08, 0x73, 0x74, 0x72,
	0x61, 0x74, 0x65, 0x67, 0x79, 0x42, 0x2f, 0x0a, 0x2b, 0x63, 0x6f, 0x6d, 0x2e, 0x67, 0x6f, 0x6f,
	0x67, 0x6c, 0x65, 0x2e, 0x64, 0x65, 0x76, 0x74, 0x6f, 0x6f, 0x6c, 0x73, 0x2e, 0x62, 0x75, 0x69,
	0x6c, 0x64, 0x2e, 0x6c, 0x69, 0x62, 0x2e, 0x72, 0x75, 0x6e, 0x74, 0x69, 0x6d, 0x65, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x50, 0x01,
}

var (
	file_src_main_protobuf_strategy_policy_proto_rawDescOnce sync.Once
	file_src_main_protobuf_strategy_policy_proto_rawDescData = file_src_main_protobuf_strategy_policy_proto_rawDesc
)

func file_src_main_protobuf_strategy_policy_proto_rawDescGZIP() []byte {
	file_src_main_protobuf_strategy_policy_proto_rawDescOnce.Do(func() {
		file_src_main_protobuf_strategy_policy_proto_rawDescData = protoimpl.X.CompressGZIP(file_src_main_protobuf_strategy_policy_proto_rawDescData)
	})
	return file_src_main_protobuf_strategy_policy_proto_rawDescData
}

var file_src_main_protobuf_strategy_policy_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_src_main_protobuf_strategy_policy_proto_goTypes = []interface{}{
	(*StrategyPolicy)(nil),        // 0: blaze.strategy_policy.StrategyPolicy
	(*MnemonicPolicy)(nil),        // 1: blaze.strategy_policy.MnemonicPolicy
	(*StrategiesForMnemonic)(nil), // 2: blaze.strategy_policy.StrategiesForMnemonic
}
var file_src_main_protobuf_strategy_policy_proto_depIdxs = []int32{
	1, // 0: blaze.strategy_policy.StrategyPolicy.mnemonic_policy:type_name -> blaze.strategy_policy.MnemonicPolicy
	1, // 1: blaze.strategy_policy.StrategyPolicy.dynamic_remote_policy:type_name -> blaze.strategy_policy.MnemonicPolicy
	1, // 2: blaze.strategy_policy.StrategyPolicy.dynamic_local_policy:type_name -> blaze.strategy_policy.MnemonicPolicy
	2, // 3: blaze.strategy_policy.MnemonicPolicy.strategy_allowlist:type_name -> blaze.strategy_policy.StrategiesForMnemonic
	4, // [4:4] is the sub-list for method output_type
	4, // [4:4] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_src_main_protobuf_strategy_policy_proto_init() }
func file_src_main_protobuf_strategy_policy_proto_init() {
	if File_src_main_protobuf_strategy_policy_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_src_main_protobuf_strategy_policy_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*StrategyPolicy); i {
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
		file_src_main_protobuf_strategy_policy_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*MnemonicPolicy); i {
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
		file_src_main_protobuf_strategy_policy_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*StrategiesForMnemonic); i {
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
			RawDescriptor: file_src_main_protobuf_strategy_policy_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_src_main_protobuf_strategy_policy_proto_goTypes,
		DependencyIndexes: file_src_main_protobuf_strategy_policy_proto_depIdxs,
		MessageInfos:      file_src_main_protobuf_strategy_policy_proto_msgTypes,
	}.Build()
	File_src_main_protobuf_strategy_policy_proto = out.File
	file_src_main_protobuf_strategy_policy_proto_rawDesc = nil
	file_src_main_protobuf_strategy_policy_proto_goTypes = nil
	file_src_main_protobuf_strategy_policy_proto_depIdxs = nil
}
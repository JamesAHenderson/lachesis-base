// Code generated by protoc-gen-go. DO NOT EDIT.
// source: event.proto

package poset

import (
	fmt "fmt"
	wire "github.com/Fantom-foundation/go-lachesis/src/inter/wire"
	proto "github.com/golang/protobuf/proto"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

type BlockSignature struct {
	Validator            []byte   `protobuf:"bytes,1,opt,name=Validator,proto3" json:"Validator,omitempty"`
	Index                int64    `protobuf:"varint,2,opt,name=Index,proto3" json:"Index,omitempty"`
	Signature            string   `protobuf:"bytes,3,opt,name=Signature,proto3" json:"Signature,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *BlockSignature) Reset()         { *m = BlockSignature{} }
func (m *BlockSignature) String() string { return proto.CompactTextString(m) }
func (*BlockSignature) ProtoMessage()    {}
func (*BlockSignature) Descriptor() ([]byte, []int) {
	return fileDescriptor_2d17a9d3f0ddf27e, []int{0}
}

func (m *BlockSignature) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_BlockSignature.Unmarshal(m, b)
}
func (m *BlockSignature) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_BlockSignature.Marshal(b, m, deterministic)
}
func (m *BlockSignature) XXX_Merge(src proto.Message) {
	xxx_messageInfo_BlockSignature.Merge(m, src)
}
func (m *BlockSignature) XXX_Size() int {
	return xxx_messageInfo_BlockSignature.Size(m)
}
func (m *BlockSignature) XXX_DiscardUnknown() {
	xxx_messageInfo_BlockSignature.DiscardUnknown(m)
}

var xxx_messageInfo_BlockSignature proto.InternalMessageInfo

func (m *BlockSignature) GetValidator() []byte {
	if m != nil {
		return m.Validator
	}
	return nil
}

func (m *BlockSignature) GetIndex() int64 {
	if m != nil {
		return m.Index
	}
	return 0
}

func (m *BlockSignature) GetSignature() string {
	if m != nil {
		return m.Signature
	}
	return ""
}

type EventBody struct {
	Transactions         [][]byte                    `protobuf:"bytes,1,rep,name=Transactions,proto3" json:"Transactions,omitempty"`
	InternalTransactions []*wire.InternalTransaction `protobuf:"bytes,2,rep,name=InternalTransactions,proto3" json:"InternalTransactions,omitempty"`
	Parents              [][]byte                    `protobuf:"bytes,3,rep,name=Parents,proto3" json:"Parents,omitempty"`
	Creator              []byte                      `protobuf:"bytes,4,opt,name=Creator,proto3" json:"Creator,omitempty"`
	Index                int64                       `protobuf:"varint,5,opt,name=Index,proto3" json:"Index,omitempty"`
	BlockSignatures      []*BlockSignature           `protobuf:"bytes,6,rep,name=BlockSignatures,proto3" json:"BlockSignatures,omitempty"`
	XXX_NoUnkeyedLiteral struct{}                    `json:"-"`
	XXX_unrecognized     []byte                      `json:"-"`
	XXX_sizecache        int32                       `json:"-"`
}

func (m *EventBody) Reset()         { *m = EventBody{} }
func (m *EventBody) String() string { return proto.CompactTextString(m) }
func (*EventBody) ProtoMessage()    {}
func (*EventBody) Descriptor() ([]byte, []int) {
	return fileDescriptor_2d17a9d3f0ddf27e, []int{1}
}

func (m *EventBody) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_EventBody.Unmarshal(m, b)
}
func (m *EventBody) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_EventBody.Marshal(b, m, deterministic)
}
func (m *EventBody) XXX_Merge(src proto.Message) {
	xxx_messageInfo_EventBody.Merge(m, src)
}
func (m *EventBody) XXX_Size() int {
	return xxx_messageInfo_EventBody.Size(m)
}
func (m *EventBody) XXX_DiscardUnknown() {
	xxx_messageInfo_EventBody.DiscardUnknown(m)
}

var xxx_messageInfo_EventBody proto.InternalMessageInfo

func (m *EventBody) GetTransactions() [][]byte {
	if m != nil {
		return m.Transactions
	}
	return nil
}

func (m *EventBody) GetInternalTransactions() []*wire.InternalTransaction {
	if m != nil {
		return m.InternalTransactions
	}
	return nil
}

func (m *EventBody) GetParents() [][]byte {
	if m != nil {
		return m.Parents
	}
	return nil
}

func (m *EventBody) GetCreator() []byte {
	if m != nil {
		return m.Creator
	}
	return nil
}

func (m *EventBody) GetIndex() int64 {
	if m != nil {
		return m.Index
	}
	return 0
}

func (m *EventBody) GetBlockSignatures() []*BlockSignature {
	if m != nil {
		return m.BlockSignatures
	}
	return nil
}

type EventMessage struct {
	Body                 *EventBody `protobuf:"bytes,1,opt,name=Body,proto3" json:"Body,omitempty"`
	Signature            string     `protobuf:"bytes,2,opt,name=Signature,proto3" json:"Signature,omitempty"`
	FlagTable            []byte     `protobuf:"bytes,3,opt,name=FlagTable,proto3" json:"FlagTable,omitempty"`
	ClothoProof          [][]byte   `protobuf:"bytes,4,rep,name=ClothoProof,proto3" json:"ClothoProof,omitempty"`
	SelfParentIndex      int64      `protobuf:"varint,5,opt,name=SelfParentIndex,proto3" json:"SelfParentIndex,omitempty"`
	OtherParentCreatorID uint64     `protobuf:"varint,6,opt,name=OtherParentCreatorID,proto3" json:"OtherParentCreatorID,omitempty"`
	OtherParentIndex     int64      `protobuf:"varint,7,opt,name=OtherParentIndex,proto3" json:"OtherParentIndex,omitempty"`
	CreatorID            uint64     `protobuf:"varint,8,opt,name=CreatorID,proto3" json:"CreatorID,omitempty"`
	TopologicalIndex     int64      `protobuf:"varint,9,opt,name=TopologicalIndex,proto3" json:"TopologicalIndex,omitempty"`
	Hash                 []byte     `protobuf:"bytes,10,opt,name=Hash,proto3" json:"Hash,omitempty"`
	XXX_NoUnkeyedLiteral struct{}   `json:"-"`
	XXX_unrecognized     []byte     `json:"-"`
	XXX_sizecache        int32      `json:"-"`
}

func (m *EventMessage) Reset()         { *m = EventMessage{} }
func (m *EventMessage) String() string { return proto.CompactTextString(m) }
func (*EventMessage) ProtoMessage()    {}
func (*EventMessage) Descriptor() ([]byte, []int) {
	return fileDescriptor_2d17a9d3f0ddf27e, []int{2}
}

func (m *EventMessage) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_EventMessage.Unmarshal(m, b)
}
func (m *EventMessage) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_EventMessage.Marshal(b, m, deterministic)
}
func (m *EventMessage) XXX_Merge(src proto.Message) {
	xxx_messageInfo_EventMessage.Merge(m, src)
}
func (m *EventMessage) XXX_Size() int {
	return xxx_messageInfo_EventMessage.Size(m)
}
func (m *EventMessage) XXX_DiscardUnknown() {
	xxx_messageInfo_EventMessage.DiscardUnknown(m)
}

var xxx_messageInfo_EventMessage proto.InternalMessageInfo

func (m *EventMessage) GetBody() *EventBody {
	if m != nil {
		return m.Body
	}
	return nil
}

func (m *EventMessage) GetSignature() string {
	if m != nil {
		return m.Signature
	}
	return ""
}

func (m *EventMessage) GetFlagTable() []byte {
	if m != nil {
		return m.FlagTable
	}
	return nil
}

func (m *EventMessage) GetClothoProof() [][]byte {
	if m != nil {
		return m.ClothoProof
	}
	return nil
}

func (m *EventMessage) GetSelfParentIndex() int64 {
	if m != nil {
		return m.SelfParentIndex
	}
	return 0
}

func (m *EventMessage) GetOtherParentCreatorID() uint64 {
	if m != nil {
		return m.OtherParentCreatorID
	}
	return 0
}

func (m *EventMessage) GetOtherParentIndex() int64 {
	if m != nil {
		return m.OtherParentIndex
	}
	return 0
}

func (m *EventMessage) GetCreatorID() uint64 {
	if m != nil {
		return m.CreatorID
	}
	return 0
}

func (m *EventMessage) GetTopologicalIndex() int64 {
	if m != nil {
		return m.TopologicalIndex
	}
	return 0
}

func (m *EventMessage) GetHash() []byte {
	if m != nil {
		return m.Hash
	}
	return nil
}

func init() {
	proto.RegisterType((*BlockSignature)(nil), "poset.BlockSignature")
	proto.RegisterType((*EventBody)(nil), "poset.EventBody")
	proto.RegisterType((*EventMessage)(nil), "poset.EventMessage")
}

func init() { proto.RegisterFile("event.proto", fileDescriptor_2d17a9d3f0ddf27e) }

var fileDescriptor_2d17a9d3f0ddf27e = []byte{
	// 433 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0x6c, 0x92, 0xdf, 0x6a, 0xdb, 0x30,
	0x14, 0xc6, 0xf1, 0x9f, 0xa6, 0xf3, 0x89, 0x59, 0x8b, 0xc8, 0x40, 0x1b, 0xbb, 0x30, 0x61, 0x17,
	0x66, 0x50, 0x1b, 0xb2, 0x07, 0x18, 0x34, 0x5b, 0x59, 0x2e, 0xca, 0x8a, 0x1a, 0x76, 0xaf, 0x38,
	0x8a, 0x6d, 0xa6, 0xea, 0x04, 0x49, 0xd9, 0x9f, 0x47, 0xd8, 0x2b, 0xef, 0x6a, 0x48, 0x6e, 0x6b,
	0x3b, 0xcb, 0x9d, 0xcf, 0x77, 0x8e, 0x3e, 0x9d, 0xef, 0x67, 0xc1, 0x54, 0xfc, 0x10, 0xca, 0x16,
	0x7b, 0x8d, 0x16, 0xc9, 0xd9, 0x1e, 0x8d, 0xb0, 0x6f, 0x96, 0x75, 0x6b, 0x9b, 0xc3, 0xa6, 0xa8,
	0xf0, 0xa1, 0xbc, 0xe1, 0xca, 0xe2, 0xc3, 0xd5, 0x0e, 0x0f, 0x6a, 0xcb, 0x6d, 0x8b, 0xaa, 0xac,
	0xf1, 0x4a, 0xf2, 0xaa, 0x11, 0xa6, 0x35, 0xa5, 0xd1, 0x55, 0xd9, 0x2a, 0x2b, 0x74, 0xf9, 0xb3,
	0xd5, 0xa2, 0x1c, 0x78, 0xcd, 0x37, 0xf0, 0xf2, 0x5a, 0x62, 0xf5, 0xfd, 0xbe, 0xad, 0x15, 0xb7,
	0x07, 0x2d, 0xc8, 0x5b, 0x48, 0xbe, 0x71, 0xd9, 0x6e, 0xb9, 0x45, 0x4d, 0x83, 0x2c, 0xc8, 0x53,
	0xd6, 0x0b, 0x64, 0x06, 0x67, 0x2b, 0xb5, 0x15, 0xbf, 0x68, 0x98, 0x05, 0x79, 0xc4, 0xba, 0xc2,
	0x9d, 0x79, 0x36, 0xa0, 0x51, 0x16, 0xe4, 0x09, 0xeb, 0x85, 0xf9, 0x9f, 0x10, 0x92, 0xcf, 0xee,
	0xce, 0x6b, 0xdc, 0xfe, 0x26, 0x73, 0x48, 0xd7, 0x9a, 0x2b, 0xc3, 0x2b, 0xb7, 0xa7, 0xa1, 0x41,
	0x16, 0xe5, 0x29, 0x1b, 0x69, 0xe4, 0x16, 0x66, 0x2b, 0xb7, 0xaf, 0xe2, 0x72, 0x34, 0x1b, 0x66,
	0x51, 0x3e, 0x5d, 0xbc, 0x2e, 0x5c, 0x8c, 0xe2, 0xc4, 0x04, 0x3b, 0x79, 0x8c, 0x50, 0x38, 0xbf,
	0xe3, 0x5a, 0x28, 0x6b, 0x68, 0xe4, 0x6f, 0x7b, 0x2a, 0x5d, 0x67, 0xa9, 0x85, 0x8f, 0x1a, 0xfb,
	0xa8, 0x4f, 0x65, 0x1f, 0xf4, 0x6c, 0x18, 0xf4, 0x23, 0x5c, 0x8c, 0x71, 0x19, 0x3a, 0xf1, 0x3b,
	0xbd, 0x2a, 0xfc, 0x4f, 0x29, 0xc6, 0x5d, 0x76, 0x3c, 0x3d, 0xff, 0x1b, 0x42, 0xea, 0x59, 0xdc,
	0x0a, 0x63, 0x78, 0x2d, 0xc8, 0x3b, 0x88, 0x1d, 0x16, 0x4f, 0x7a, 0xba, 0xb8, 0x7c, 0xb4, 0x79,
	0xc6, 0xc5, 0x7c, 0x77, 0x0c, 0x38, 0x3c, 0x02, 0xec, 0xba, 0x37, 0x92, 0xd7, 0x6b, 0xbe, 0x91,
	0x1d, 0xfe, 0x94, 0xf5, 0x02, 0xc9, 0x60, 0xba, 0x94, 0x68, 0x1b, 0xbc, 0xd3, 0x88, 0x3b, 0x1a,
	0x7b, 0x02, 0x43, 0x89, 0xe4, 0x70, 0x71, 0x2f, 0xe4, 0xae, 0x83, 0x32, 0x4c, 0x7d, 0x2c, 0x93,
	0x05, 0xcc, 0xbe, 0xda, 0x46, 0xe8, 0x4e, 0x7b, 0x64, 0xb5, 0xfa, 0x44, 0x27, 0x59, 0x90, 0xc7,
	0xec, 0x64, 0x8f, 0xbc, 0x87, 0xcb, 0x81, 0xde, 0xd9, 0x9f, 0x7b, 0xfb, 0xff, 0x74, 0x97, 0xa4,
	0x37, 0x7d, 0xe1, 0x4d, 0x93, 0x91, 0xd3, 0x1a, 0xf7, 0x28, 0xb1, 0x6e, 0x2b, 0x2e, 0x3b, 0xa7,
	0xa4, 0x73, 0x3a, 0xd6, 0x09, 0x81, 0xf8, 0x0b, 0x37, 0x0d, 0x05, 0x8f, 0xc3, 0x7f, 0x6f, 0x26,
	0xfe, 0xcd, 0x7f, 0xf8, 0x17, 0x00, 0x00, 0xff, 0xff, 0xbf, 0xcc, 0x5e, 0x53, 0x4e, 0x03, 0x00,
	0x00,
}

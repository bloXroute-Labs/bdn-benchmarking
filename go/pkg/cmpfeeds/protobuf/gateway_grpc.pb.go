// Code generated by protoc-gen-go-grpc. DO NOT EDIT.

package cmdfeeds

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// GatewayClient is the client API for Gateway service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type GatewayClient interface {
	BlxrTx(ctx context.Context, in *BlxrTxRequest, opts ...grpc.CallOption) (*BlxrTxReply, error)
	BlxrBatchTX(ctx context.Context, in *BlxrBatchTXRequest, opts ...grpc.CallOption) (*BlxrBatchTXReply, error)
	Peers(ctx context.Context, in *PeersRequest, opts ...grpc.CallOption) (*PeersReply, error)
	TxStoreSummary(ctx context.Context, in *TxStoreRequest, opts ...grpc.CallOption) (*TxStoreReply, error)
	GetTx(ctx context.Context, in *GetBxTransactionRequest, opts ...grpc.CallOption) (*GetBxTransactionResponse, error)
	Stop(ctx context.Context, in *StopRequest, opts ...grpc.CallOption) (*StopReply, error)
	Version(ctx context.Context, in *VersionRequest, opts ...grpc.CallOption) (*VersionReply, error)
	Status(ctx context.Context, in *StatusRequest, opts ...grpc.CallOption) (*StatusResponse, error)
	Subscriptions(ctx context.Context, in *SubscriptionsRequest, opts ...grpc.CallOption) (*SubscriptionsReply, error)
	DisconnectInboundPeer(ctx context.Context, in *DisconnectInboundPeerRequest, opts ...grpc.CallOption) (*DisconnectInboundPeerReply, error)
	NewTxs(ctx context.Context, in *TxsRequest, opts ...grpc.CallOption) (Gateway_NewTxsClient, error)
	PendingTxs(ctx context.Context, in *TxsRequest, opts ...grpc.CallOption) (Gateway_PendingTxsClient, error)
	NewBlocks(ctx context.Context, in *BlocksRequest, opts ...grpc.CallOption) (Gateway_NewBlocksClient, error)
	BdnBlocks(ctx context.Context, in *BlocksRequest, opts ...grpc.CallOption) (Gateway_BdnBlocksClient, error)
}

type gatewayClient struct {
	cc grpc.ClientConnInterface
}

func NewGatewayClient(cc grpc.ClientConnInterface) GatewayClient {
	return &gatewayClient{cc}
}

func (c *gatewayClient) BlxrTx(ctx context.Context, in *BlxrTxRequest, opts ...grpc.CallOption) (*BlxrTxReply, error) {
	out := new(BlxrTxReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/BlxrTx", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) BlxrBatchTX(ctx context.Context, in *BlxrBatchTXRequest, opts ...grpc.CallOption) (*BlxrBatchTXReply, error) {
	out := new(BlxrBatchTXReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/BlxrBatchTX", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) Peers(ctx context.Context, in *PeersRequest, opts ...grpc.CallOption) (*PeersReply, error) {
	out := new(PeersReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/Peers", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) TxStoreSummary(ctx context.Context, in *TxStoreRequest, opts ...grpc.CallOption) (*TxStoreReply, error) {
	out := new(TxStoreReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/TxStoreSummary", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) GetTx(ctx context.Context, in *GetBxTransactionRequest, opts ...grpc.CallOption) (*GetBxTransactionResponse, error) {
	out := new(GetBxTransactionResponse)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/GetTx", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) Stop(ctx context.Context, in *StopRequest, opts ...grpc.CallOption) (*StopReply, error) {
	out := new(StopReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/Stop", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) Version(ctx context.Context, in *VersionRequest, opts ...grpc.CallOption) (*VersionReply, error) {
	out := new(VersionReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/Version", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) Status(ctx context.Context, in *StatusRequest, opts ...grpc.CallOption) (*StatusResponse, error) {
	out := new(StatusResponse)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/Status", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) Subscriptions(ctx context.Context, in *SubscriptionsRequest, opts ...grpc.CallOption) (*SubscriptionsReply, error) {
	out := new(SubscriptionsReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/Subscriptions", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) DisconnectInboundPeer(ctx context.Context, in *DisconnectInboundPeerRequest, opts ...grpc.CallOption) (*DisconnectInboundPeerReply, error) {
	out := new(DisconnectInboundPeerReply)
	err := c.cc.Invoke(ctx, "/gateway.Gateway/DisconnectInboundPeer", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gatewayClient) NewTxs(ctx context.Context, in *TxsRequest, opts ...grpc.CallOption) (Gateway_NewTxsClient, error) {
	stream, err := c.cc.NewStream(ctx, &Gateway_ServiceDesc.Streams[0], "/gateway.Gateway/NewTxs", opts...)
	if err != nil {
		return nil, err
	}
	x := &gatewayNewTxsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Gateway_NewTxsClient interface {
	Recv() (*TxsReply, error)
	grpc.ClientStream
}

type gatewayNewTxsClient struct {
	grpc.ClientStream
}

func (x *gatewayNewTxsClient) Recv() (*TxsReply, error) {
	m := new(TxsReply)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *gatewayClient) PendingTxs(ctx context.Context, in *TxsRequest, opts ...grpc.CallOption) (Gateway_PendingTxsClient, error) {
	stream, err := c.cc.NewStream(ctx, &Gateway_ServiceDesc.Streams[1], "/gateway.Gateway/PendingTxs", opts...)
	if err != nil {
		return nil, err
	}
	x := &gatewayPendingTxsClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Gateway_PendingTxsClient interface {
	Recv() (*TxsReply, error)
	grpc.ClientStream
}

type gatewayPendingTxsClient struct {
	grpc.ClientStream
}

func (x *gatewayPendingTxsClient) Recv() (*TxsReply, error) {
	m := new(TxsReply)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *gatewayClient) NewBlocks(ctx context.Context, in *BlocksRequest, opts ...grpc.CallOption) (Gateway_NewBlocksClient, error) {
	stream, err := c.cc.NewStream(ctx, &Gateway_ServiceDesc.Streams[2], "/gateway.Gateway/NewBlocks", opts...)
	if err != nil {
		return nil, err
	}
	x := &gatewayNewBlocksClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Gateway_NewBlocksClient interface {
	Recv() (*BlocksReply, error)
	grpc.ClientStream
}

type gatewayNewBlocksClient struct {
	grpc.ClientStream
}

func (x *gatewayNewBlocksClient) Recv() (*BlocksReply, error) {
	m := new(BlocksReply)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *gatewayClient) BdnBlocks(ctx context.Context, in *BlocksRequest, opts ...grpc.CallOption) (Gateway_BdnBlocksClient, error) {
	stream, err := c.cc.NewStream(ctx, &Gateway_ServiceDesc.Streams[3], "/gateway.Gateway/BdnBlocks", opts...)
	if err != nil {
		return nil, err
	}
	x := &gatewayBdnBlocksClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type Gateway_BdnBlocksClient interface {
	Recv() (*BlocksReply, error)
	grpc.ClientStream
}

type gatewayBdnBlocksClient struct {
	grpc.ClientStream
}

func (x *gatewayBdnBlocksClient) Recv() (*BlocksReply, error) {
	m := new(BlocksReply)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// GatewayServer is the server API for Gateway service.
// All implementations must embed UnimplementedGatewayServer
// for forward compatibility
type GatewayServer interface {
	BlxrTx(context.Context, *BlxrTxRequest) (*BlxrTxReply, error)
	BlxrBatchTX(context.Context, *BlxrBatchTXRequest) (*BlxrBatchTXReply, error)
	Peers(context.Context, *PeersRequest) (*PeersReply, error)
	TxStoreSummary(context.Context, *TxStoreRequest) (*TxStoreReply, error)
	GetTx(context.Context, *GetBxTransactionRequest) (*GetBxTransactionResponse, error)
	Stop(context.Context, *StopRequest) (*StopReply, error)
	Version(context.Context, *VersionRequest) (*VersionReply, error)
	Status(context.Context, *StatusRequest) (*StatusResponse, error)
	Subscriptions(context.Context, *SubscriptionsRequest) (*SubscriptionsReply, error)
	DisconnectInboundPeer(context.Context, *DisconnectInboundPeerRequest) (*DisconnectInboundPeerReply, error)
	NewTxs(*TxsRequest, Gateway_NewTxsServer) error
	PendingTxs(*TxsRequest, Gateway_PendingTxsServer) error
	NewBlocks(*BlocksRequest, Gateway_NewBlocksServer) error
	BdnBlocks(*BlocksRequest, Gateway_BdnBlocksServer) error
	mustEmbedUnimplementedGatewayServer()
}

// UnimplementedGatewayServer must be embedded to have forward compatible implementations.
type UnimplementedGatewayServer struct {
}

func (UnimplementedGatewayServer) BlxrTx(context.Context, *BlxrTxRequest) (*BlxrTxReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method BlxrTx not implemented")
}
func (UnimplementedGatewayServer) BlxrBatchTX(context.Context, *BlxrBatchTXRequest) (*BlxrBatchTXReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method BlxrBatchTX not implemented")
}
func (UnimplementedGatewayServer) Peers(context.Context, *PeersRequest) (*PeersReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Peers not implemented")
}
func (UnimplementedGatewayServer) TxStoreSummary(context.Context, *TxStoreRequest) (*TxStoreReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method TxStoreSummary not implemented")
}
func (UnimplementedGatewayServer) GetTx(context.Context, *GetBxTransactionRequest) (*GetBxTransactionResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method GetTx not implemented")
}
func (UnimplementedGatewayServer) Stop(context.Context, *StopRequest) (*StopReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Stop not implemented")
}
func (UnimplementedGatewayServer) Version(context.Context, *VersionRequest) (*VersionReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Version not implemented")
}
func (UnimplementedGatewayServer) Status(context.Context, *StatusRequest) (*StatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Status not implemented")
}
func (UnimplementedGatewayServer) Subscriptions(context.Context, *SubscriptionsRequest) (*SubscriptionsReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Subscriptions not implemented")
}
func (UnimplementedGatewayServer) DisconnectInboundPeer(context.Context, *DisconnectInboundPeerRequest) (*DisconnectInboundPeerReply, error) {
	return nil, status.Errorf(codes.Unimplemented, "method DisconnectInboundPeer not implemented")
}
func (UnimplementedGatewayServer) NewTxs(*TxsRequest, Gateway_NewTxsServer) error {
	return status.Errorf(codes.Unimplemented, "method NewTxs not implemented")
}
func (UnimplementedGatewayServer) PendingTxs(*TxsRequest, Gateway_PendingTxsServer) error {
	return status.Errorf(codes.Unimplemented, "method PendingTxs not implemented")
}
func (UnimplementedGatewayServer) NewBlocks(*BlocksRequest, Gateway_NewBlocksServer) error {
	return status.Errorf(codes.Unimplemented, "method NewBlocks not implemented")
}
func (UnimplementedGatewayServer) BdnBlocks(*BlocksRequest, Gateway_BdnBlocksServer) error {
	return status.Errorf(codes.Unimplemented, "method BdnBlocks not implemented")
}
func (UnimplementedGatewayServer) mustEmbedUnimplementedGatewayServer() {}

// UnsafeGatewayServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to GatewayServer will
// result in compilation errors.
type UnsafeGatewayServer interface {
	mustEmbedUnimplementedGatewayServer()
}

func RegisterGatewayServer(s grpc.ServiceRegistrar, srv GatewayServer) {
	s.RegisterService(&Gateway_ServiceDesc, srv)
}

func _Gateway_BlxrTx_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BlxrTxRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).BlxrTx(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/BlxrTx",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).BlxrTx(ctx, req.(*BlxrTxRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_BlxrBatchTX_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BlxrBatchTXRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).BlxrBatchTX(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/BlxrBatchTX",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).BlxrBatchTX(ctx, req.(*BlxrBatchTXRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_Peers_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PeersRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).Peers(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/Peers",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).Peers(ctx, req.(*PeersRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_TxStoreSummary_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TxStoreRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).TxStoreSummary(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/TxStoreSummary",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).TxStoreSummary(ctx, req.(*TxStoreRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_GetTx_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetBxTransactionRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).GetTx(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/GetTx",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).GetTx(ctx, req.(*GetBxTransactionRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_Stop_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StopRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).Stop(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/Stop",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).Stop(ctx, req.(*StopRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_Version_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(VersionRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).Version(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/Version",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).Version(ctx, req.(*VersionRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_Status_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(StatusRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).Status(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/Status",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).Status(ctx, req.(*StatusRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_Subscriptions_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(SubscriptionsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).Subscriptions(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/Subscriptions",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).Subscriptions(ctx, req.(*SubscriptionsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_DisconnectInboundPeer_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DisconnectInboundPeerRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServer).DisconnectInboundPeer(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/gateway.Gateway/DisconnectInboundPeer",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServer).DisconnectInboundPeer(ctx, req.(*DisconnectInboundPeerRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _Gateway_NewTxs_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(TxsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(GatewayServer).NewTxs(m, &gatewayNewTxsServer{stream})
}

type Gateway_NewTxsServer interface {
	Send(*TxsReply) error
	grpc.ServerStream
}

type gatewayNewTxsServer struct {
	grpc.ServerStream
}

func (x *gatewayNewTxsServer) Send(m *TxsReply) error {
	return x.ServerStream.SendMsg(m)
}

func _Gateway_PendingTxs_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(TxsRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(GatewayServer).PendingTxs(m, &gatewayPendingTxsServer{stream})
}

type Gateway_PendingTxsServer interface {
	Send(*TxsReply) error
	grpc.ServerStream
}

type gatewayPendingTxsServer struct {
	grpc.ServerStream
}

func (x *gatewayPendingTxsServer) Send(m *TxsReply) error {
	return x.ServerStream.SendMsg(m)
}

func _Gateway_NewBlocks_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(BlocksRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(GatewayServer).NewBlocks(m, &gatewayNewBlocksServer{stream})
}

type Gateway_NewBlocksServer interface {
	Send(*BlocksReply) error
	grpc.ServerStream
}

type gatewayNewBlocksServer struct {
	grpc.ServerStream
}

func (x *gatewayNewBlocksServer) Send(m *BlocksReply) error {
	return x.ServerStream.SendMsg(m)
}

func _Gateway_BdnBlocks_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(BlocksRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(GatewayServer).BdnBlocks(m, &gatewayBdnBlocksServer{stream})
}

type Gateway_BdnBlocksServer interface {
	Send(*BlocksReply) error
	grpc.ServerStream
}

type gatewayBdnBlocksServer struct {
	grpc.ServerStream
}

func (x *gatewayBdnBlocksServer) Send(m *BlocksReply) error {
	return x.ServerStream.SendMsg(m)
}

// Gateway_ServiceDesc is the grpc.ServiceDesc for Gateway service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var Gateway_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "gateway.Gateway",
	HandlerType: (*GatewayServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "BlxrTx",
			Handler:    _Gateway_BlxrTx_Handler,
		},
		{
			MethodName: "BlxrBatchTX",
			Handler:    _Gateway_BlxrBatchTX_Handler,
		},
		{
			MethodName: "Peers",
			Handler:    _Gateway_Peers_Handler,
		},
		{
			MethodName: "TxStoreSummary",
			Handler:    _Gateway_TxStoreSummary_Handler,
		},
		{
			MethodName: "GetTx",
			Handler:    _Gateway_GetTx_Handler,
		},
		{
			MethodName: "Stop",
			Handler:    _Gateway_Stop_Handler,
		},
		{
			MethodName: "Version",
			Handler:    _Gateway_Version_Handler,
		},
		{
			MethodName: "Status",
			Handler:    _Gateway_Status_Handler,
		},
		{
			MethodName: "Subscriptions",
			Handler:    _Gateway_Subscriptions_Handler,
		},
		{
			MethodName: "DisconnectInboundPeer",
			Handler:    _Gateway_DisconnectInboundPeer_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "NewTxs",
			Handler:       _Gateway_NewTxs_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "PendingTxs",
			Handler:       _Gateway_PendingTxs_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "NewBlocks",
			Handler:       _Gateway_NewBlocks_Handler,
			ServerStreams: true,
		},
		{
			StreamName:    "BdnBlocks",
			Handler:       _Gateway_BdnBlocks_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "gateway.proto",
}

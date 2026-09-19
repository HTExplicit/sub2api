package extensionv1

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const MaxPayloadBytes = 4 * 1024 * 1024
const pluginService = "sub2api.extensions.v1.Plugin"
const hostService = "sub2api.extensions.v1.Host"

// The RPC envelope is protobuf BytesValue. Versioned JSON contracts inside it
// keep optional extensions independent from the official transport proto.
type Client struct{ conn grpc.ClientConnInterface }

func NewClient(conn grpc.ClientConnInterface) *Client { return &Client{conn: conn} }

func (c *Client) Invoke(ctx context.Context, in Invocation) (Result, error) {
	return c.call(ctx, "/"+pluginService+"/Invoke", in)
}

func (c *Client) Call(ctx context.Context, in HostInvocation) (Result, error) {
	return c.call(ctx, "/"+hostService+"/Call", in)
}

func (c *Client) call(ctx context.Context, method string, in any) (Result, error) {
	var result Result
	if c == nil || c.conn == nil {
		return result, status.Error(codes.Unavailable, "extension connection unavailable")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return result, err
	}
	if len(raw) > MaxPayloadBytes {
		return result, status.Error(codes.ResourceExhausted, "extension payload too large")
	}
	out := &wrapperspb.BytesValue{}
	if err := c.conn.Invoke(ctx, method, wrapperspb.Bytes(raw), out); err != nil {
		return result, err
	}
	if len(out.Value) > MaxPayloadBytes {
		return result, status.Error(codes.ResourceExhausted, "extension result too large")
	}
	if err := json.Unmarshal(out.Value, &result); err != nil {
		return result, status.Error(codes.DataLoss, "invalid extension result")
	}
	return result, nil
}

type rpcServer interface {
	dispatch(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}
type rpcAdapter struct {
	invoke func(context.Context, []byte) (Result, error)
}

func (a *rpcAdapter) dispatch(ctx context.Context, in *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if in == nil || len(in.Value) > MaxPayloadBytes {
		return nil, status.Error(codes.ResourceExhausted, "extension payload too large")
	}
	result, err := a.invoke(ctx, in.Value)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, status.Error(codes.Internal, "extension result encoding failed")
	}
	if len(raw) > MaxPayloadBytes {
		return nil, status.Error(codes.ResourceExhausted, "extension result too large")
	}
	return wrapperspb.Bytes(raw), nil
}

func RegisterPlugin(server grpc.ServiceRegistrar, handler Handler) {
	register(server, pluginService, "Invoke", &rpcAdapter{invoke: func(ctx context.Context, raw []byte) (Result, error) {
		var in Invocation
		if json.Unmarshal(raw, &in) != nil || !IsCapability(in.Capability) || in.Operation == "" {
			return Result{}, status.Error(codes.InvalidArgument, "invalid extension invocation")
		}
		return handler.Invoke(ctx, in)
	}})
}

func RegisterHost(server grpc.ServiceRegistrar, handler HostHandler) {
	register(server, hostService, "Call", &rpcAdapter{invoke: func(ctx context.Context, raw []byte) (Result, error) {
		var in HostInvocation
		if json.Unmarshal(raw, &in) != nil || in.Operation == "" {
			return Result{}, status.Error(codes.InvalidArgument, "invalid extension host invocation")
		}
		return handler.Call(ctx, in)
	}})
}

func register(server grpc.ServiceRegistrar, service, method string, adapter *rpcAdapter) {
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: service, HandlerType: (*rpcServer)(nil),
		Methods: []grpc.MethodDesc{{MethodName: method, Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := &wrapperspb.BytesValue{}
			if err := decode(in); err != nil {
				return nil, err
			}
			call := func(ctx context.Context, req any) (any, error) {
				return srv.(rpcServer).dispatch(ctx, req.(*wrapperspb.BytesValue))
			}
			if interceptor == nil {
				return call(ctx, in)
			}
			return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + service + "/" + method}, call)
		}}},
	}, adapter)
}

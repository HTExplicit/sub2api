package extensionv1

import (
	"context"
	"encoding/json"
	"sync"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	hcplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Module is the only domain implementation loaded into a plugin process. Its
// configuration and policy never execute inside the host process.
type Module interface {
	Handler
	ValidateConfig(context.Context, json.RawMessage) (json.RawMessage, error)
	ApplyConfig(context.Context, json.RawMessage) error
	Status(context.Context) (json.RawMessage, error)
}

type HostReceiver interface{ SetHost(*Client) }

type Definition struct {
	ID           string
	Version      string
	Capabilities []string
}

type Runtime struct {
	pluginv1.UnimplementedTransportPluginServer
	definition Definition
	module     Module
	mu         sync.Mutex
	broker     *hcplugin.GRPCBroker
	hostConn   *grpc.ClientConn
}

func NewRuntime(definition Definition, module Module) *Runtime {
	return &Runtime{definition: definition, module: module}
}
func Serve(definition Definition, module Module)             { pluginv1.Serve(NewRuntime(definition, module)) }
func (r *Runtime) SetHostBroker(broker *hcplugin.GRPCBroker) { r.broker = broker }
func (r *Runtime) RegisterAdditionalServices(server grpc.ServiceRegistrar) {
	RegisterPlugin(server, r.module)
}

func (r *Runtime) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	return &pluginv1.GetInfoResponse{PluginId: r.definition.ID, PluginVersion: r.definition.Version,
		ProtocolVersion: pluginv1.ProtocolVersion, TransportApiVersion: pluginv1.TransportAPIVersion, Capabilities: r.definition.Capabilities}, nil
}

func (r *Runtime) Health(ctx context.Context, _ *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	raw, err := r.module.Status(ctx)
	if err != nil {
		return &pluginv1.HealthResponse{Healthy: false, Message: "extension status unavailable"}, nil
	}
	return &pluginv1.HealthResponse{Healthy: true, StatusJson: string(raw)}, nil
}

func (r *Runtime) ValidateConfig(ctx context.Context, in *pluginv1.ValidateConfigRequest) (*pluginv1.ValidateConfigResponse, error) {
	if in == nil || len(in.ConfigJson) > MaxPayloadBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid configuration")
	}
	raw, err := r.module.ValidateConfig(ctx, in.ConfigJson)
	if err != nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: err.Error()}, nil
	}
	return &pluginv1.ValidateConfigResponse{Valid: true, NormalizedConfigJson: raw}, nil
}

func (r *Runtime) ApplyConfig(ctx context.Context, in *pluginv1.ApplyConfigRequest) (*pluginv1.ApplyConfigResponse, error) {
	if in == nil || len(in.ConfigJson) > MaxPayloadBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid configuration")
	}
	raw, err := r.module.ValidateConfig(ctx, in.ConfigJson)
	if err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	if err := r.module.ApplyConfig(ctx, raw); err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	return &pluginv1.ApplyConfigResponse{Applied: true}, nil
}

func (r *Runtime) InitHostServices(_ context.Context, in *pluginv1.InitHostServicesRequest) (*pluginv1.InitHostServicesResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.broker == nil || in == nil || in.HostServiceApiVersion != pluginv1.HostServiceAPIVersion {
		return nil, status.Error(codes.FailedPrecondition, "compatible host broker required")
	}
	connection, err := r.broker.Dial(in.HostServiceId)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "host broker unavailable")
	}
	if receiver, ok := r.module.(HostReceiver); ok {
		receiver.SetHost(NewClient(connection))
	}
	previous := r.hostConn
	r.hostConn = connection
	if previous != nil {
		_ = previous.Close()
	}
	return &pluginv1.InitHostServicesResponse{Ready: true}, nil
}

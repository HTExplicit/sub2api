package extensionv1

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type capturedRPCRegistration struct {
	description    *grpc.ServiceDesc
	implementation any
}

func (r *capturedRPCRegistration) RegisterService(description *grpc.ServiceDesc, implementation any) {
	r.description, r.implementation = description, implementation
}

func TestExtensionRPCRejectsInvalidDispatchTypes(t *testing.T) {
	registration := &capturedRPCRegistration{}
	RegisterPlugin(registration, testHandler{})
	handler := registration.description.Methods[0].Handler
	decode := func(any) error { return nil }
	_, err := handler(struct{}{}, context.Background(), decode, nil)
	if status.Code(err) != codes.Internal {
		t.Fatalf("invalid handler did not fail closed: %v", err)
	}
	for _, request := range []any{struct{}{}, (*wrapperspb.BytesValue)(nil)} {
		interceptor := func(ctx context.Context, _ any, _ *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
			return next(ctx, request)
		}
		_, err := handler(registration.implementation, context.Background(), decode, interceptor)
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("invalid interceptor request did not fail closed: %v", err)
		}
	}
}

type testHandler struct{}

func (testHandler) Invoke(ctx context.Context, in Invocation) (Result, error) {
	if in.Operation == "wait" {
		<-ctx.Done()
		return Result{}, status.FromContextError(ctx.Err()).Err()
	}
	var req SchedulingRequest
	if err := Decode(in.Payload, &req); err != nil {
		return Result{}, err
	}
	raw, err := Encode(SchedulingDecision{Allowed: false, Reason: "ticket_missing", Scope: req.Model})
	return Result{Payload: raw}, err
}

func TestExtensionRPCContractAndCancellation(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	RegisterPlugin(server, testHandler{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpc.NewClient("passthrough:///extension", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := NewClient(connection)
	raw, _ := Encode(SchedulingRequest{Account: Account{ID: 17, Platform: "openai", Type: "oauth"}, Model: "model-a"})
	out, err := client.Invoke(context.Background(), Invocation{Capability: CapabilityScheduling, Operation: "admit", Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	var decision SchedulingDecision
	if err := json.Unmarshal(out.Payload, &decision); err != nil {
		t.Fatal(err)
	}
	if decision.Allowed || decision.Reason != "ticket_missing" || decision.Scope != "model-a" {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	_, err = client.Invoke(context.Background(), Invocation{Capability: "shell.exec", Operation: "run", Payload: raw})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown capability accepted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.Invoke(ctx, Invocation{Capability: CapabilityScheduling, Operation: "wait", Payload: raw})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("cancellation not propagated: %v", err)
	}
}

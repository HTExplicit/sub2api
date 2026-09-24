package tickets

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/internal/nativeapi"
)

type lifecycleHost struct {
	entered  chan context.Context
	finished chan struct{}
	calls    atomic.Int32
}

func (h *lifecycleHost) Call(ctx context.Context, in extensionv1.HostInvocation) (extensionv1.Result, error) {
	if in.Operation == extensionv1.HostAccountList {
		h.calls.Add(1)
		h.entered <- ctx
		<-ctx.Done()
		h.finished <- struct{}{}
		return extensionv1.Result{}, ctx.Err()
	}
	return extensionv1.Result{Payload: json.RawMessage(`{}`)}, nil
}

func TestNativeModuleLifecycleStartsOnceAndDrains(t *testing.T) {
	host := &lifecycleHost{entered: make(chan context.Context, 2), finished: make(chan struct{}, 2)}
	module := NewModule()
	module.SetHost(host)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	defer func() { _ = module.Stop(ctx) }()
	if err := module.ApplyConfig(ctx, json.RawMessage(`{"enabled":false}`)); err != nil {
		t.Fatal(err)
	}
	if host.calls.Load() != 0 {
		t.Fatal("configuration started background work before Start")
	}
	if err := module.Start(ctx); err != nil {
		t.Fatal(err)
	}
	var first context.Context
	select {
	case first = <-host.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := module.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if host.calls.Load() != 1 {
		t.Fatal("duplicate Start launched another migration")
	}
	if err := module.ApplyConfig(ctx, json.RawMessage(`{"enabled":null}`)); err == nil {
		t.Fatal("invalid config accepted")
	}
	if first.Err() != nil {
		t.Fatal("invalid config canceled the active epoch")
	}
	if err := module.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-host.finished:
	default:
		t.Fatal("Stop returned before background work finished")
	}
	if first.Err() == nil {
		t.Fatal("Stop retained the old epoch")
	}
	if err := module.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := module.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-host.entered:
		if second == first || second.Err() != nil {
			t.Fatal("restart reused canceled epoch")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestNativeHostCallerPreservesStateCAS(t *testing.T) {
	host := &memoryHost{state: map[string]extensionv1.StateResult{}, lease: map[string]int64{}}
	state := State{Schema: 2, Identity: "owner", Phase: "stopped", Failures: 2}
	revision, err := writeState(context.Background(), host, 7, "gpt-6-astra", state, 0)
	if err != nil || revision != 1 {
		t.Fatalf("initial CAS: revision=%d error=%v", revision, err)
	}
	loaded, got, err := readState(context.Background(), host, 7, "gpt-6-astra")
	if err != nil || got != revision || loaded.Phase != "stopped" || loaded.Failures != 2 {
		t.Fatal("direct host changed the persisted state")
	}
	if _, err = writeState(context.Background(), host, 7, "gpt-6-astra", State{Schema: 2, Phase: "ready"}, 0); err == nil {
		t.Fatal("stale revision overwrote state")
	}
}

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type nativeCodexReceiptRepository struct {
	// Any config write, stored-source read or business lease is unexpected.
	NativeCodexRepository
	metadata NativeCodexMetadata
	loads    int
	load     func(context.Context) (*NativeCodexMetadata, error)
}

func (r *nativeCodexReceiptRepository) LoadNativeCodexMetadata(ctx context.Context) (*NativeCodexMetadata, error) {
	r.loads++
	if r.load != nil {
		return r.load(ctx)
	}
	metadata := r.metadata
	return &metadata, nil
}

type nativeCodexReceiptModule struct{ nativeCodexModule }

func (nativeCodexReceiptModule) Stop(context.Context) error { return nil }

func nativeCodexReceiptFixture(t *testing.T) (*OpenAIGatewayService, *nativeCodexSnapshot, *nativeCodexReceiptRepository) {
	t.Helper()
	// Preserve field order, whitespace and the trailing newline in the receipt.
	raw := json.RawMessage("{\n  \"models\": [\"gpt-6-astra\"],\n  \"enabled\": false\n}\n")
	sum := sha256.Sum256(raw)
	metadata := NativeCodexMetadata{
		ID: 17, ConfigVersion: 9_007_199_254_740_993, RuntimeGeneration: 9_223_372_036_854_775_807,
		ConfigSHA256: hex.EncodeToString(sum[:]),
	}
	repo := &nativeCodexReceiptRepository{metadata: metadata}
	epoch, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	snapshot := &nativeCodexSnapshot{
		metadata: &metadata, raw: raw, ctx: epoch, cancel: cancel,
		module: nativeCodexReceiptModule{}, host: &nativeCodexHost{},
	}
	runtime := &NativeCodexRuntime{
		root: context.Background(), started: true, repo: repo,
		factory: func(context.Context) (json.RawMessage, error) {
			t.Fatal("a receipt read must not rebuild configuration")
			return nil, ErrNativeCodexRuntimeUnavailable
		},
	}
	runtime.snapshot.Store(snapshot)
	return &OpenAIGatewayService{nativeCodexRuntime: runtime}, snapshot, repo
}

func requireNativeCodexReceiptUnavailable(t *testing.T, raw json.RawMessage, metadata NativeCodexMetadata, err error) {
	t.Helper()
	require.Equal(t, ErrNativeCodexRuntimeUnavailable, err)
	require.Nil(t, raw)
	require.Zero(t, metadata)
}

func TestNativeCodexConfigurationReceiptCopiesExactSnapshot(t *testing.T) {
	service, snapshot, repo := nativeCodexReceiptFixture(t)
	wantRaw := append(json.RawMessage(nil), snapshot.raw...)
	wantMetadata := *snapshot.metadata
	raw, metadata, err := service.NativeCodexConfiguration(context.Background())
	require.NoError(t, err)
	require.Equal(t, wantRaw, raw)
	require.Equal(t, wantMetadata, metadata)
	require.Equal(t, 1, repo.loads)
	sum := sha256.Sum256(raw)
	require.Equal(t, hex.EncodeToString(sum[:]), metadata.ConfigSHA256)

	raw[0] = '!'
	metadata.ID = 99
	metadata.ConfigVersion = 1
	metadata.RuntimeGeneration = 1
	metadata.ConfigSHA256 = strings.Repeat("0", 64)
	require.NotEqual(t, wantRaw, raw)
	require.NotEqual(t, wantMetadata, metadata)
	require.Equal(t, wantRaw, snapshot.raw)
	require.Equal(t, wantMetadata, *snapshot.metadata)
	require.Equal(t, wantMetadata, repo.metadata)
	secondRaw, secondMetadata, err := service.NativeCodexConfiguration(context.Background())
	require.NoError(t, err)
	require.Equal(t, wantRaw, secondRaw)
	require.Equal(t, wantMetadata, secondMetadata)
	require.Equal(t, 2, repo.loads)
}

func TestNativeCodexConfigurationReceiptRejectsInactiveRuntime(t *testing.T) {
	for _, name := range []string{
		"nil service", "missing runtime", "not started", "stopped", "missing repository",
		"missing snapshot", "missing metadata", "missing epoch", "canceled epoch", "nil request context", "canceled request",
	} {
		t.Run(name, func(t *testing.T) {
			service, snapshot, repo := nativeCodexReceiptFixture(t)
			ctx := context.Background()
			switch name {
			case "nil service":
				service = nil
			case "missing runtime":
				service.nativeCodexRuntime = nil
			case "not started":
				service.nativeCodexRuntime.started = false
			case "stopped":
				require.NoError(t, service.nativeCodexRuntime.Stop(ctx))
				require.Same(t, snapshot, service.nativeCodexRuntime.current())
			case "missing repository":
				service.nativeCodexRuntime.repo = nil
			case "missing snapshot":
				service.nativeCodexRuntime.snapshot.Store(nil)
			case "missing metadata":
				snapshot.metadata = nil
			case "missing epoch":
				snapshot.ctx = nil
			case "canceled epoch":
				snapshot.cancel()
			case "nil request context":
				ctx = nil
			case "canceled request":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			raw, metadata, err := service.NativeCodexConfiguration(ctx)
			requireNativeCodexReceiptUnavailable(t, raw, metadata, err)
			require.Zero(t, repo.loads)
		})
	}
}

func TestNativeCodexConfigurationReceiptRejectsInvalidSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		change func(*nativeCodexSnapshot)
	}{
		{"zero ID", func(s *nativeCodexSnapshot) { s.metadata.ID = 0 }},
		{"negative ID", func(s *nativeCodexSnapshot) { s.metadata.ID = -1 }},
		{"zero version", func(s *nativeCodexSnapshot) { s.metadata.ConfigVersion = 0 }},
		{"negative version", func(s *nativeCodexSnapshot) { s.metadata.ConfigVersion = -1 }},
		{"zero generation", func(s *nativeCodexSnapshot) { s.metadata.RuntimeGeneration = 0 }},
		{"negative generation", func(s *nativeCodexSnapshot) { s.metadata.RuntimeGeneration = -1 }},
		{"missing hash", func(s *nativeCodexSnapshot) { s.metadata.ConfigSHA256 = "" }},
		{"uppercase hash", func(s *nativeCodexSnapshot) { s.metadata.ConfigSHA256 = strings.ToUpper(s.metadata.ConfigSHA256) }},
		{"invalid hash", func(s *nativeCodexSnapshot) { s.metadata.ConfigSHA256 = strings.Repeat("x", 64) }},
		{"wrong hash", func(s *nativeCodexSnapshot) { s.metadata.ConfigSHA256 = strings.Repeat("0", 64) }},
		{"changed raw bytes", func(s *nativeCodexSnapshot) { s.raw = append(s.raw, '\n') }},
		{"empty raw", func(s *nativeCodexSnapshot) {
			s.raw = nil
			sum := sha256.Sum256(nil)
			s.metadata.ConfigSHA256 = hex.EncodeToString(sum[:])
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, snapshot, repo := nativeCodexReceiptFixture(t)
			tt.change(snapshot)
			// Matching persistence must not make an invalid snapshot acceptable.
			repo.metadata = *snapshot.metadata
			raw, metadata, err := service.NativeCodexConfiguration(context.Background())
			requireNativeCodexReceiptUnavailable(t, raw, metadata, err)
			require.Zero(t, repo.loads)
		})
	}
}

func TestNativeCodexConfigurationReceiptRejectsUnavailableFence(t *testing.T) {
	for _, name := range []string{"read failure", "missing metadata", "ID changed", "version changed", "generation changed", "hash changed"} {
		t.Run(name, func(t *testing.T) {
			service, _, repo := nativeCodexReceiptFixture(t)
			switch name {
			case "read failure":
				repo.load = func(context.Context) (*NativeCodexMetadata, error) {
					return &repo.metadata, errors.New("private repository failure detail")
				}
			case "missing metadata":
				repo.load = func(context.Context) (*NativeCodexMetadata, error) { return nil, nil }
			case "ID changed":
				repo.metadata.ID++
			case "version changed":
				repo.metadata.ConfigVersion++
			case "generation changed":
				repo.metadata.RuntimeGeneration--
			case "hash changed":
				repo.metadata.ConfigSHA256 = strings.Repeat("0", 64)
			}
			raw, metadata, err := service.NativeCodexConfiguration(context.Background())
			requireNativeCodexReceiptUnavailable(t, raw, metadata, err)
			require.Equal(t, 1, repo.loads)
		})
	}
}

func TestNativeCodexConfigurationReceiptRejectsCancellationDuringRead(t *testing.T) {
	for _, name := range []string{"request", "epoch"} {
		t.Run(name, func(t *testing.T) {
			service, snapshot, repo := nativeCodexReceiptFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			repo.load = func(loadCtx context.Context) (*NativeCodexMetadata, error) {
				require.Same(t, ctx, loadCtx)
				if name == "request" {
					cancel()
				} else {
					snapshot.cancel()
				}
				// A reader may return a row despite cancellation; no receipt may escape.
				metadata := repo.metadata
				return &metadata, nil
			}
			raw, metadata, err := service.NativeCodexConfiguration(ctx)
			requireNativeCodexReceiptUnavailable(t, raw, metadata, err)
			require.Equal(t, 1, repo.loads)
		})
	}
}

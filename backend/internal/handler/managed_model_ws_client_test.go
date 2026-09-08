package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestManagedModelWSReservedSelectorsRejectRawFramesBeforeExistingHook(t *testing.T) {
	for _, payload := range []string{
		`{"type":"response.create","model":"s2pub-g23-m0123456789abcdef"}`,
		`{"type":"response.create","model":"gpt-5.4","Model":" S2PuB-private "}`,
		`{"type":"response.create","model":"gpt-5.4","model":"s2pub-other-group"}`,
		`{"type":"session.update","session":{"model":"s2pub-g33-m0123456789abcdef"}}`,
		`{"type":"session.update","Session":{"Model":" S2PUB-anything "}}`,
		`{"type":"session.update","session":{"model":"gpt-5.4","Model":"s2pub-hidden"}}`,
		`{"type":"response.create","model":"gpt-5.4","session":{"model":"s2pub-hidden"}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			called := false
			prepare := composeManagedModelWSClientFrameGuard(func(_ int, body []byte, _ string) ([]byte, string, error) {
				called = true
				return body, "unexpected", nil
			})
			body, publicModel, err := prepare(2, []byte(payload), "gpt-5.4")
			require.ErrorIs(t, err, service.ErrManagedModelRouteUnavailable)
			require.ErrorIs(t, err, errOpenAIWSLocalTurnRejection)
			var closeErr *service.OpenAIWSClientCloseError
			require.ErrorAs(t, err, &closeErr)
			require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
			require.False(t, shouldReportOpenAIWSProxyAccountFailure(err), "client-reserved names must not penalize the account")
			require.False(t, called, "reject before observer, managed preparation, or account mapping")
			require.Nil(t, body)
			require.Empty(t, publicModel)
		})
	}
}

func TestManagedModelWSReservedGuardPreservesPrivateAndCindyBytes(t *testing.T) {
	prepare := composeManagedModelWSClientFrameGuard(nil)
	for _, payload := range []string{
		` {"type":"response.create","model":"private-alias","model":"private-alias"} `,
		`{"type":"response.create","model":"openai/gpt-5.6-luna","input":"s2pub- is ordinary text here"}`,
		`{"type":"session.update","session":{"model":"anthropic/claude-sonnet-4"}}`,
		`{"type":"response.create","model":"gpt-5.4","tools":[{"type":"function","name":"inspect","parameters":{"properties":{"model":{"default":"s2pub-tool-data"}}}}]}`,
	} {
		t.Run(payload, func(t *testing.T) {
			raw := []byte(payload)
			body, publicModel, err := prepare(2, raw, "private-alias")
			require.NoError(t, err)
			require.Equal(t, raw, body)
			require.Same(t, &raw[0], &body[0], "nil prior hooks must keep the exact original byte slice")
			require.Empty(t, publicModel)
		})
	}
}

func TestManagedModelWSReservedGuardDoesNotRecheckTrustedPreparedOutput(t *testing.T) {
	raw := []byte(`{"type":"session.update","session":{"model":"public-model"}}`)
	prepared := []byte(`{"type":"session.update","session":{"model":"s2pub-internal-wire-model"}}`)
	calls := 0
	prepare := composeManagedModelWSClientFrameGuard(func(turn int, body []byte, fallback string) ([]byte, string, error) {
		calls++
		require.Equal(t, 2, turn)
		require.Same(t, &raw[0], &body[0])
		require.Equal(t, "prior-public-model", fallback)
		return prepared, "public-model", nil
	})
	body, publicModel, err := prepare(2, raw, "prior-public-model")
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.Same(t, &prepared[0], &body[0])
	require.Equal(t, "public-model", publicModel)

	wantErr := errors.New("existing preparation rejection")
	prepare = composeManagedModelWSClientFrameGuard(func(_ int, _ []byte, _ string) ([]byte, string, error) {
		return nil, "", wantErr
	})
	_, _, err = prepare(2, raw, "prior-public-model")
	require.ErrorIs(t, err, wantErr)
}

func TestManagedModelWSReservedGuardComposesLegalManagedPreparation(t *testing.T) {
	guard, _, account := newManagedModelWSFixture()
	prepare := composeManagedModelWSClientFrameGuard(func(turn int, payload []byte, fallback string) ([]byte, string, error) {
		body, request, _, err := guard.prepareFrame(context.Background(), turn, payload, fallback, account)
		if err != nil {
			return nil, "", err
		}
		return body, request.Route.PublicModel, nil
	})
	body, publicModel, err := prepare(1, []byte(`{"type":"response.create","model":"gpt-5.4-high"}`), "")
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", publicModel)
	require.Equal(t, "gpt-5.4", gjson.GetBytes(body, "model").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	// The first service-side call sees the handler's canonical public frame,
	// not a selector, and may safely pass through this wrapper a second time.
	_, publicModel, err = prepare(1, body, publicModel)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", publicModel)
	body, publicModel, err = prepare(2, []byte(`{"type":"session.update","session":{"model":"public-gpt"}}`), publicModel)
	require.NoError(t, err)
	require.Equal(t, "gpt-5.4", publicModel)
	require.Equal(t, "verified-wire-model", gjson.GetBytes(body, "session.model").String())
}

func TestManagedModelWSReservedSelectorFirstFrameRejectedForPrivateAndCindy(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformCindy} {
		t.Run(platform, func(t *testing.T) {
			selector := service.ManagedModelSelector(23, "gpt-5.4")
			runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:            `{"type":"response.create","model":"` + selector + `"}`,
				accountModelMapping:     map[string]any{selector: "gpt-5.4"},
				group:                   &service.Group{ID: 4201, Platform: platform, Status: service.StatusActive, IsExclusive: true},
				firstFrameCloseExpected: true,
			})
		})
	}
}

func TestManagedModelWSReservedSelectorSubsequentPrivateTurnRejected(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			selector := service.ManagedModelSelector(23, "gpt-5.4")
			runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:            `{"type":"response.create","model":"gpt-5.4"}`,
				secondPayload:           `{"type":"response.create","model":"` + selector + `"}`,
				accountModelMapping:     map[string]any{"gpt-5.4": "gpt-5.4", selector: "gpt-5.4"},
				group:                   &service.Group{ID: 4201, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true},
				ingressMode:             mode,
				secondTurnCloseExpected: true,
			})
		})
	}
}

func TestManagedModelWSReservedSelectorSessionUpdateRejectedAtRawBoundary(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			selector := service.ManagedModelSelector(23, "gpt-5.4")
			// The runner also asserts the model-rejection reason. Native ingress
			// already rejects session.update as an unsupported type, so merely
			// observing status 1008 would not prove the reserved guard ran first.
			runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:            `{"type":"response.create","model":"gpt-5.4"}`,
				secondPayload:           `{"type":"session.update","session":{"model":"` + selector + `"}}`,
				accountModelMapping:     map[string]any{"gpt-5.4": "gpt-5.4", selector: "gpt-5.4"},
				group:                   &service.Group{ID: 4201, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true},
				ingressMode:             mode,
				secondTurnCloseExpected: true,
			})
		})
	}
}

func TestManagedModelWSReservedSelectorAliasCannotSwitchPrivateSecondTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		for _, viaChannel := range []bool{true, false} {
			name := mode + "/account_alias"
			if viaChannel {
				name = mode + "/channel_alias"
			}
			t.Run(name, func(t *testing.T) {
				selector := service.ManagedModelSelector(23, "gpt-5.4")
				mapping := map[string]any{"gpt-5.4": "gpt-5.4", selector: "gpt-5.4"}
				var channelMapping map[string]string
				if viaChannel {
					channelMapping = map[string]string{"private-alias": selector}
				} else {
					mapping["private-alias"] = selector
				}
				// Both raw frames are ordinary names, so only the per-turn
				// mapping guard can close this already established connection.
				runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload:            `{"type":"response.create","model":"gpt-5.4"}`,
					secondPayload:           `{"type":"response.create","model":"private-alias"}`,
					accountModelMapping:     mapping,
					channelMapping:          channelMapping,
					billingModelSource:      service.BillingModelSourceRequested,
					group:                   &service.Group{ID: 4201, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true},
					ingressMode:             mode,
					secondTurnCloseExpected: true,
				})
			})
		}
	}
}

func TestManagedModelWSReservedGuardKeepsLegalPrivateTurnsWorking(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:  `{"type":"response.create","model":"gpt-5.4"}`,
				secondPayload: `{"type":"response.create","model":"gpt-5.4","input":"s2pub- is just text"}`,
				group:         &service.Group{ID: 4201, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true},
				ingressMode:   mode,
			})
			require.Len(t, got.clientEvents, 2)
			require.Len(t, got.upstreamPayloads, 2)
		})
	}
}

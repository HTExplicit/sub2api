package service

import (
	"context"
	"net/http"
	"strings"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

type openAIRefusalRecoveryWSWriteFunc func(context.Context, coderws.MessageType, []byte) error

type openAIRefusalRecoveryWSFrame struct {
	messageType coderws.MessageType
	payload     []byte
}

type openAIRefusalRecoveryWSOutput struct {
	matcher         *OpenAIRefusalMatcher
	state           *openAIRefusalStreamState
	guardReplay     bool
	passthrough     bool
	semanticWrite   bool
	downstreamWrite bool
	bufferedBytes   int
	buffered        []openAIRefusalRecoveryWSFrame
	write           openAIRefusalRecoveryWSWriteFunc
	onBufferLimit   func()
}

type openAIRefusalRecoveryWSFrameConn struct {
	inner  openaiwsv2.FrameConn
	output *openAIRefusalRecoveryWSOutput
}

func (c *openAIRefusalRecoveryWSFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	return c.inner.ReadFrame(ctx)
}

func (c *openAIRefusalRecoveryWSFrameConn) WriteFrame(ctx context.Context, messageType coderws.MessageType, payload []byte) error {
	return c.output.Write(ctx, messageType, payload)
}

func (c *openAIRefusalRecoveryWSFrameConn) Close() error {
	return c.inner.Close()
}

var _ openaiwsv2.FrameConn = (*openAIRefusalRecoveryWSFrameConn)(nil)

func newOpenAIRefusalRecoveryWSOutput(matcher *OpenAIRefusalMatcher, guardReplay bool, write openAIRefusalRecoveryWSWriteFunc, onBufferLimit func()) *openAIRefusalRecoveryWSOutput {
	output := &openAIRefusalRecoveryWSOutput{
		matcher:       matcher,
		guardReplay:   guardReplay,
		write:         write,
		onBufferLimit: onBufferLimit,
	}
	output.resetTurn()
	return output
}

func (o *openAIRefusalRecoveryWSOutput) Write(ctx context.Context, messageType coderws.MessageType, payload []byte) error {
	if o == nil || o.write == nil {
		return nil
	}
	if messageType != coderws.MessageText || o.passthrough {
		return o.writeDirect(ctx, messageType, payload)
	}
	eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
	if eventType == "" {
		if err := o.flushBuffered(ctx); err != nil {
			return err
		}
		o.passthrough = true
		return o.writeDirect(ctx, messageType, payload)
	}
	if o.bufferedBytes+len(payload) > maxOpenAIRefusalStreamBufferBytes {
		if o.onBufferLimit != nil {
			o.onBufferLimit()
		}
		if err := o.flushBuffered(ctx); err != nil {
			return err
		}
		o.passthrough = true
		return o.writeDirect(ctx, messageType, payload)
	}

	if o.state != nil {
		action, replacementSSE, err := o.state.observe(eventType, payload)
		if err != nil {
			if flushErr := o.flushBuffered(ctx); flushErr != nil {
				return flushErr
			}
			o.passthrough = true
			return o.writeDirect(ctx, messageType, payload)
		}
		switch action {
		case openAIRefusalStreamHold:
			o.buffer(messageType, payload)
			return nil
		case openAIRefusalStreamPass:
			if err := o.flushBuffered(ctx); err != nil {
				return err
			}
			err := o.writeDirect(ctx, messageType, payload)
			if err == nil && openAIWSPassthroughIsTerminalOutput(payload) {
				o.resetTurn()
			}
			return err
		case openAIRefusalStreamReplace:
			o.dropTurnBuffer()
			var replacementFrames [][]byte
			forEachOpenAISSEDataPayload(string(replacementSSE), func(data []byte) {
				replacementFrames = append(replacementFrames, append([]byte(nil), data...))
			})
			for _, frame := range replacementFrames {
				if err := o.writeDirect(ctx, coderws.MessageText, frame); err != nil {
					return err
				}
			}
			o.resetTurn()
			return nil
		}
	}

	if o.guardReplay && !openAIRefusalRecoveryWSSemanticEvent(eventType, payload) && !openAIWSPassthroughIsTerminalOutput(payload) {
		o.buffer(messageType, payload)
		return nil
	}
	if err := o.flushBuffered(ctx); err != nil {
		return err
	}
	err := o.writeDirect(ctx, messageType, payload)
	if err == nil && openAIWSPassthroughIsTerminalOutput(payload) {
		o.resetTurn()
	}
	return err
}

func (o *openAIRefusalRecoveryWSOutput) writeDirect(ctx context.Context, messageType coderws.MessageType, payload []byte) error {
	if err := o.write(ctx, messageType, payload); err != nil {
		return err
	}
	o.downstreamWrite = true
	if messageType == coderws.MessageText {
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		if openAIRefusalRecoveryWSSemanticEvent(eventType, payload) {
			o.semanticWrite = true
		}
	}
	return nil
}

func (o *openAIRefusalRecoveryWSOutput) buffer(messageType coderws.MessageType, payload []byte) {
	o.bufferedBytes += len(payload)
	o.buffered = append(o.buffered, openAIRefusalRecoveryWSFrame{
		messageType: messageType,
		payload:     append([]byte(nil), payload...),
	})
}

func (o *openAIRefusalRecoveryWSOutput) flushBuffered(ctx context.Context) error {
	for _, frame := range o.buffered {
		if err := o.writeDirect(ctx, frame.messageType, frame.payload); err != nil {
			return err
		}
	}
	o.dropTurnBuffer()
	return nil
}

func (o *openAIRefusalRecoveryWSOutput) DropTurn() {
	if o == nil {
		return
	}
	o.resetTurn()
}

func (o *openAIRefusalRecoveryWSOutput) SemanticOutputStarted() bool {
	return o != nil && o.semanticWrite
}

func (o *openAIRefusalRecoveryWSOutput) DownstreamOutputStarted() bool {
	return o != nil && o.downstreamWrite
}

func (o *openAIRefusalRecoveryWSOutput) WriteRetryableFailure(ctx context.Context, upstreamPayload ...[]byte) error {
	if o == nil {
		return nil
	}
	o.dropTurnBuffer()
	payload := OpenAIWSRetryableFailureEvent()
	if len(upstreamPayload) > 0 {
		if sanitized, ok := sanitizeOpenAICyberPolicyFailedEvent(upstreamPayload[0]); ok {
			payload = sanitized
		}
	}
	err := o.write(ctx, coderws.MessageText, payload)
	o.resetTurn()
	return err
}

func (o *openAIRefusalRecoveryWSOutput) resetTurn() {
	o.dropTurnBuffer()
	o.passthrough = false
	o.semanticWrite = false
	o.downstreamWrite = false
	if o.matcher != nil {
		o.state = newOpenAIRefusalStreamState(o.matcher)
	} else {
		o.state = nil
	}
}

func (o *openAIRefusalRecoveryWSOutput) dropTurnBuffer() {
	o.buffered = nil
	o.bufferedBytes = 0
}

func openAIRefusalRecoveryWSSemanticEvent(eventType string, payload []byte) bool {
	if openAIRefusalEventRequiresPassthrough(eventType, payload) {
		return true
	}
	switch eventType {
	case "", "response.created", "response.in_progress", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return false
	case "response.output_item.added", "response.output_item.done", "response.content_part.added":
		return false
	default:
		return strings.Contains(eventType, ".delta") ||
			strings.HasPrefix(eventType, "response.output_text") ||
			strings.HasPrefix(eventType, "response.refusal") ||
			strings.HasPrefix(eventType, "response.reasoning")
	}
}

func newOpenAIWSCyberRecoveryError(payload []byte, headers http.Header, replaySafe bool) error {
	if replaySafe {
		return NewOpenAICyberFailoverError(payload, headers)
	}
	return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "Temporary upstream failure; please retry", NewOpenAICyberFailoverError(payload, headers))
}

func OpenAIWSRetryableFailureEvent() []byte {
	return []byte(`{"type":"response.failed","response":{"id":"resp_retryable_failure","object":"response","status":"failed","output":[],"error":{"type":"server_error","code":"upstream_retry_exhausted","message":"Temporary upstream failure","retryable":true}}}`)
}

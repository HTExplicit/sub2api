package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/codextransport"
	codexws "github.com/Wei-Shaw/sub2api/internal/pkg/codexws"
	coderws "github.com/coder/websocket"
)

func (d *coderOpenAIWSClientDialer) dialStrictCodex(ctx context.Context, target string, headers http.Header, proxy string, binding codextransport.Binding) (openAIWSClientConn, int, http.Header, error) {
	key := "codex:" + binding.Key() + ":" + proxy
	d.proxyMu.Lock()
	entry := d.proxyClients[key]
	if entry == nil {
		var parsed *url.URL
		var err error
		if proxy != "" {
			parsed, err = url.Parse(proxy)
			if err != nil {
				d.proxyMu.Unlock()
				return nil, 0, nil, err
			}
		}
		rt, err := codextransport.New(binding, codextransport.Options{ProxyURL: parsed, MaxIdleConns: openAIWSProxyTransportMaxIdleConns,
			MaxIdleConnsPerHost: openAIWSProxyTransportMaxIdleConnsPerHost, IdleConnTimeout: openAIWSProxyTransportIdleConnTimeout})
		if err != nil {
			d.proxyMu.Unlock()
			return nil, 0, nil, err
		}
		entry = &openAIWSProxyClientEntry{client: &http.Client{Transport: rt}}
		d.proxyClients[key] = entry
		d.proxyMisses.Add(1)
	} else {
		d.proxyHits.Add(1)
	}
	entry.lastUsedUnixNano = time.Now().UnixNano()
	d.cleanupProxyClientsLocked(entry.lastUsedUnixNano)
	d.ensureProxyClientCapacityLocked()
	d.proxyMu.Unlock()
	wrapped := &strictCodexWSConn{}
	conn, resp, err := codexws.Dial(ctx, target, &codexws.DialOptions{HTTPClient: entry.client, HTTPHeader: headers,
		CompressionMode: codexws.CompressionContextTakeover, ClientMaxWindowBits: true,
		OnPingReceived: func(context.Context, []byte) bool { wrapped.upstreamPings.Add(1); return true }})
	status := 0
	responseHeaders := http.Header(nil)
	if resp != nil {
		status = resp.StatusCode
		responseHeaders = cloneHeader(resp.Header)
	}
	if err != nil {
		var body []byte
		if resp != nil && resp.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
		}
		if status == 0 {
			err = &codextransport.Failure{Err: err}
		}
		return nil, status, responseHeaders, &openAIWSHandshakeError{Body: body, Err: err}
	}
	conn.SetReadLimit(openAIWSMessageReadLimitBytes)
	wrapped.conn = conn
	return wrapped, 0, responseHeaders, nil
}

type strictCodexWSConn struct {
	conn          *codexws.Conn
	upstreamPings atomic.Int64
}

func (c *strictCodexWSConn) WriteJSON(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return strictCodexWSError(c.conn.Write(ctx, codexws.MessageText, data))
}
func (c *strictCodexWSConn) ReadMessage(ctx context.Context) ([]byte, error) {
	_, data, err := c.conn.Read(ctx)
	return data, strictCodexWSError(err)
}
func (c *strictCodexWSConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	kind, data, err := c.conn.Read(ctx)
	return coderws.MessageType(kind), data, strictCodexWSError(err)
}
func (c *strictCodexWSConn) WriteFrame(ctx context.Context, kind coderws.MessageType, data []byte) error {
	return strictCodexWSError(c.conn.Write(ctx, codexws.MessageType(kind), data))
}
func (c *strictCodexWSConn) Ping(ctx context.Context) error {
	return strictCodexWSError(c.conn.Ping(ctx))
}
func (c *strictCodexWSConn) Close() error {
	_ = c.conn.Close(codexws.StatusNormalClosure, "")
	return nil
}
func (c *strictCodexWSConn) CloseNow() error                     { return c.conn.CloseNow() }
func (c *strictCodexWSConn) RequiresReaderLoop() bool            { return true }
func (c *strictCodexWSConn) SupportsIdlePingWithoutReader() bool { return false }
func (c *strictCodexWSConn) UpstreamPingCount() int64            { return c.upstreamPings.Load() }

type codexCompatibleCloseError struct {
	original error
	compat   coderws.CloseError
}

func (e *codexCompatibleCloseError) Error() string   { return e.original.Error() }
func (e *codexCompatibleCloseError) Unwrap() []error { return []error{e.original, e.compat} }
func strictCodexWSError(err error) error {
	if err == nil {
		return nil
	}
	if status := codexws.CloseStatus(err); status != -1 {
		err = &codexCompatibleCloseError{original: err, compat: coderws.CloseError{Code: coderws.StatusCode(status), Reason: err.Error()}}
	}
	return &codextransport.Failure{Err: err}
}

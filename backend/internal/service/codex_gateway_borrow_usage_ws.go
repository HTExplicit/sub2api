package service

import (
	"context"
	"sync"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
)

// Wrap the final native WS relay, after policy rewriting. Handshakes and
// blocked client frames never enter this observer.
type codexBorrowUsageFrameConn struct {
	openaiwsv2.FrameConn
	service   *CodexGatewayBorrowService
	accountID int64
	applied   bool
	mu        sync.Mutex
	current   *codexBorrowUsageTracker
}

func (c *codexBorrowUsageFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, payload []byte) error {
	c.mu.Lock()
	if gjson.GetBytes(payload, "type").String() == "response.create" {
		c.current.finish(nil)
		c.current = c.service.beginUsage(ctx, c.accountID, gjson.GetBytes(payload, "model").String(), "ws", c.applied)
	}
	tracker := c.current
	c.mu.Unlock()
	err := c.FrameConn.WriteFrame(ctx, kind, payload)
	if err != nil {
		c.mu.Lock()
		tracker.finish(err)
		c.mu.Unlock()
	}
	return err
}

func (c *codexBorrowUsageFrameConn) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	kind, payload, err := c.FrameConn.ReadFrame(ctx)
	c.mu.Lock()
	c.current.observe(payload)
	if err != nil {
		c.current.finish(err)
	} else if c.current.terminalObserved() {
		c.current.finish(nil)
	}
	c.mu.Unlock()
	return kind, payload, err
}

func (c *codexBorrowUsageFrameConn) finish() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.current.finish(nil)
}

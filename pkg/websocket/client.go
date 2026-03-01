package websocket

import (
	"context"
	"time"

	ws "github.com/coder/websocket"
)

const (
	writeTimeout   = 10 * time.Second
	sendBufferSize = 256
)

// Client represents a single WebSocket connection managed by a Hub.
type Client struct {
	SessionID string
	SubjectID string
	Rooms     map[string]bool
	Meta      map[string]any

	hub  *Hub
	conn *ws.Conn
	send chan []byte
}

// readPump reads messages from the WebSocket connection until it closes.
// On exit it unregisters the client from the hub.
func (c *Client) readPump(ctx context.Context, onMessage func(*Client, []byte)) {
	defer c.hub.unregister(c)

	for {
		_, msg, err := c.conn.Read(ctx)
		if err != nil {
			return
		}
		if onMessage != nil {
			onMessage(c, msg)
		}
	}
}

// writePump writes messages from the send channel to the WebSocket connection.
// It exits when the send channel is closed or the context is cancelled.
func (c *Client) writePump(ctx context.Context) {
	defer c.conn.CloseNow()

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				c.conn.Close(ws.StatusNormalClosure, "")
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := c.conn.Write(writeCtx, ws.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

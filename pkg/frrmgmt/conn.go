package frrmgmt

import "context"

// Conn manages a Unix socket connection to mgmtd_fe.sock with automatic reconnect.
// Reconnect uses exponential backoff: 100ms → 200ms → ... → 5s cap, ±10% jitter.
// On reconnect, any existing session is invalidated; the Session layer must re-establish.
type Conn struct {
	// TODO: implement in Step 5
}

// Dial returns a Conn that connects (and reconnects) to sockPath.
// The connection attempt happens in the background; Dial does not block.
func Dial(ctx context.Context, sockPath string) *Conn {
	panic("not implemented")
}

// Send writes payload as a single framed message. Returns ErrNotConnected if
// the socket is currently down.
func (c *Conn) Send(payload []byte) error {
	panic("not implemented")
}

// Frames returns a channel delivering raw incoming payloads (frame header stripped).
// The channel is never closed; it blocks when no data is available.
func (c *Conn) Frames() <-chan []byte {
	panic("not implemented")
}

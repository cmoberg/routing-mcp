package frrmgmt

import "context"

// Dispatcher routes incoming frames to pending request channels and fans out
// async NOTIFY messages to subscribers.
type Dispatcher struct {
	// TODO: implement in Step 6
}

// NewDispatcher starts the dispatch loop reading from frames.
func NewDispatcher(frames <-chan []byte) *Dispatcher {
	panic("not implemented")
}

// NextReqID returns a unique, monotonically increasing request ID.
func (d *Dispatcher) NextReqID() uint64 {
	panic("not implemented")
}

// Expect registers a pending request. The returned channel receives all reply
// frames for reqID. If multi is true, frames with more=1 accumulate until
// a frame with more=0 arrives.
func (d *Dispatcher) Expect(ctx context.Context, reqID uint64, multi bool) <-chan []byte {
	panic("not implemented")
}

// Cancel removes a pending request. Any late-arriving reply is discarded.
func (d *Dispatcher) Cancel(reqID uint64) {
	panic("not implemented")
}

// Notifications returns a channel that receives all inbound NOTIFY frames.
func (d *Dispatcher) Notifications() <-chan []byte {
	panic("not implemented")
}

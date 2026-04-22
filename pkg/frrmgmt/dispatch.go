package frrmgmt

import (
	"context"
	"encoding/binary"
	"sync"
	"sync/atomic"
)

const (
	notifyBufSize = 64
	replyBufSize  = 16
	// moreOffset is the byte offset of the More field in TreeDataFixed:
	// 24-byte MsgHeader + PartialError(1) + ResultType(1) = 26.
	moreOffset = 26
)

type pending struct {
	ch    chan []byte
	multi bool
	done  chan struct{} // closed when the entry leaves the map
}

// Dispatcher routes incoming frames to pending request channels and fans out
// async NOTIFY messages to subscribers.
type Dispatcher struct {
	mu       sync.Mutex
	reqs     map[uint64]*pending
	notifyCh chan []byte
	nextID   atomic.Uint64
}

// NewDispatcher starts the dispatch loop reading from frames.
func NewDispatcher(frames <-chan []byte) *Dispatcher {
	d := &Dispatcher{
		reqs:     make(map[uint64]*pending),
		notifyCh: make(chan []byte, notifyBufSize),
	}
	go d.dispatchLoop(frames)
	return d
}

// NextReqID returns a unique, monotonically increasing request ID.
func (d *Dispatcher) NextReqID() uint64 {
	return d.nextID.Add(1)
}

// Expect registers a pending request. The returned channel receives all reply
// frames for reqID. For multi=false the channel is closed after one frame;
// for multi=true it is closed when a frame with more=0 arrives.
// If ctx is cancelled before the last frame arrives, Cancel is called automatically.
func (d *Dispatcher) Expect(ctx context.Context, reqID uint64, multi bool) <-chan []byte {
	p := &pending{
		ch:    make(chan []byte, replyBufSize),
		multi: multi,
		done:  make(chan struct{}),
	}
	d.mu.Lock()
	d.reqs[reqID] = p
	d.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			d.Cancel(reqID)
		case <-p.done:
		}
	}()

	return p.ch
}

// Cancel removes a pending request. Any late-arriving reply is discarded.
func (d *Dispatcher) Cancel(reqID uint64) {
	d.mu.Lock()
	p, ok := d.reqs[reqID]
	if ok {
		delete(d.reqs, reqID)
	}
	d.mu.Unlock()
	if ok {
		close(p.done)
	}
}

// Notifications returns a channel that receives all inbound NOTIFY frames.
func (d *Dispatcher) Notifications() <-chan []byte {
	return d.notifyCh
}

func (d *Dispatcher) dispatchLoop(frames <-chan []byte) {
	for payload := range frames {
		if len(payload) < 24 {
			continue
		}
		code := binary.LittleEndian.Uint16(payload[0:2])
		reqID := binary.LittleEndian.Uint64(payload[16:24])

		if code == CodeNotify {
			select {
			case d.notifyCh <- payload:
			default:
			}
			continue
		}

		d.mu.Lock()
		p, ok := d.reqs[reqID]
		if !ok {
			d.mu.Unlock()
			continue
		}
		more := p.multi && len(payload) > moreOffset && payload[moreOffset] != 0
		if !more {
			delete(d.reqs, reqID)
		}
		d.mu.Unlock()

		if !more {
			p.ch <- payload
			close(p.ch)
			close(p.done)
		} else {
			select {
			case p.ch <- payload:
			default:
			}
		}
	}

	// frames channel closed; signal all remaining pending requests.
	d.mu.Lock()
	remaining := d.reqs
	d.reqs = make(map[uint64]*pending)
	d.mu.Unlock()
	for _, p := range remaining {
		close(p.ch)
		close(p.done)
	}
}

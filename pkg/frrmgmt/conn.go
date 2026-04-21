package frrmgmt

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	backoffBase   = 100 * time.Millisecond
	backoffMax    = 5 * time.Second
	jitterFactor  = 0.1 // ±10%
	framesBufSize = 64
)

// Conn manages a persistent Unix socket connection to mgmtd_fe.sock.
// Reconnects automatically using exponential backoff (100ms→5s) with ±10% jitter.
// On reconnect, any active session is invalidated; the Session layer must re-establish.
type Conn struct {
	sockPath string
	ctx      context.Context
	cancel   context.CancelFunc

	connMu  sync.Mutex
	sock    net.Conn // nil while disconnected

	writeMu sync.Mutex // serialises concurrent Send calls

	framesCh chan []byte

	// injectable for testing
	dialFn  func(ctx context.Context, path string) (net.Conn, error)
	sleepFn func(d time.Duration) // blocks for d; implementations should respect ctx
	rnd     *rand.Rand
}

// Dial returns a Conn that connects (and reconnects) to sockPath.
// Connection attempts happen in the background; Dial does not block.
func Dial(ctx context.Context, sockPath string) *Conn {
	ctx, cancel := context.WithCancel(ctx)
	c := &Conn{
		sockPath: sockPath,
		ctx:      ctx,
		cancel:   cancel,
		framesCh: make(chan []byte, framesBufSize),
		dialFn: func(ctx context.Context, path string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	// sleepFn is ctx-aware so context cancellation wakes a sleeping reconnect loop.
	c.sleepFn = func(d time.Duration) {
		timer := time.NewTimer(d)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	go c.connectLoop()
	return c
}

// Send writes payload as one framed message to mgmtd.
// Returns ErrNotConnected if the socket is currently down.
func (c *Conn) Send(payload []byte) error {
	c.connMu.Lock()
	sock := c.sock
	c.connMu.Unlock()
	if sock == nil {
		return ErrNotConnected
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(sock, payload)
}

// Frames returns the channel on which incoming raw payloads are delivered
// (frame header stripped). The channel is never closed.
func (c *Conn) Frames() <-chan []byte {
	return c.framesCh
}

// IsConnected reports whether the socket is currently established.
func (c *Conn) IsConnected() bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.sock != nil
}

// Close cancels the connection and stops all reconnect attempts.
func (c *Conn) Close() {
	c.cancel()
	c.connMu.Lock()
	if c.sock != nil {
		c.sock.Close()
	}
	c.connMu.Unlock()
}

// connectLoop runs in a goroutine and maintains the socket connection.
func (c *Conn) connectLoop() {
	attempt := 0
	for {
		if c.ctx.Err() != nil {
			return
		}

		sock, err := c.dialFn(c.ctx, c.sockPath)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			c.sleepFn(c.nextBackoff(attempt))
			attempt++
			continue
		}

		attempt = 0
		c.connMu.Lock()
		c.sock = sock
		c.connMu.Unlock()

		// Close the socket when ctx is cancelled to unblock any blocking ReadFrame.
		watchDone := make(chan struct{})
		go func() {
			select {
			case <-c.ctx.Done():
				sock.Close()
			case <-watchDone:
			}
		}()

		c.readLoop(sock)
		close(watchDone)

		c.connMu.Lock()
		c.sock = nil
		c.connMu.Unlock()
	}
}

// readLoop reads frames from sock until an error occurs (disconnect or ctx cancel).
func (c *Conn) readLoop(sock net.Conn) {
	for {
		payload, err := ReadFrame(sock)
		if err != nil {
			return
		}
		select {
		case c.framesCh <- payload:
		case <-c.ctx.Done():
			return
		}
	}
}

// nextBackoff returns the delay for the given attempt (0-indexed):
// backoffBase * 2^attempt, capped at backoffMax, with ±jitterFactor jitter.
func (c *Conn) nextBackoff(attempt int) time.Duration {
	d := backoffBase
	for i := 0; i < attempt; i++ {
		d *= 2
		if d > backoffMax {
			d = backoffMax
			break
		}
	}
	jitter := 1.0 - jitterFactor + c.rnd.Float64()*(jitterFactor*2)
	return time.Duration(float64(d) * jitter)
}

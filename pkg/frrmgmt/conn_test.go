package frrmgmt

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestUnitConnReconnectBackoff tests nextBackoff directly with a fixed RNG seed,
// verifying the exponential sequence is capped at backoffMax with ±jitterFactor bounds.
func TestUnitConnReconnectBackoff(t *testing.T) {
	c := &Conn{rnd: rand.New(rand.NewSource(0))}

	cases := []struct {
		attempt int
		base    time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1600 * time.Millisecond},
		{5, 3200 * time.Millisecond},
		{6, 5000 * time.Millisecond}, // first capped
		{7, 5000 * time.Millisecond}, // still capped
		{99, 5000 * time.Millisecond}, // far beyond cap
	}

	for _, tc := range cases {
		got := c.nextBackoff(tc.attempt)
		lo := time.Duration(float64(tc.base) * (1 - jitterFactor))
		hi := time.Duration(float64(tc.base) * (1 + jitterFactor))
		if got < lo || got > hi {
			t.Errorf("attempt %d: got %v, want in [%v, %v]", tc.attempt, got, lo, hi)
		}
	}
}

// TestUnitConnSendNotConnected asserts Send returns ErrNotConnected before any
// connection is established.
func TestUnitConnSendNotConnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// dialFn that never succeeds, sleepFn that returns immediately.
	c := &Conn{
		sockPath: "/nonexistent/mgmtd_fe.sock",
		ctx:      ctx,
		cancel:   cancel,
		framesCh: make(chan []byte, framesBufSize),
		dialFn:   func(_ context.Context, _ string) (net.Conn, error) { return nil, errors.New("refused") },
		sleepFn:  func(_ time.Duration) {},
		rnd:      rand.New(rand.NewSource(0)),
	}
	go c.connectLoop()

	if err := c.Send([]byte("hello")); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
}

// TestUnitConnAutoReconnect verifies that Conn reconnects to a new listener
// after the first socket is closed, without the caller doing anything.
func TestUnitConnAutoReconnect(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	// First listener.
	l1, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()

	// Accept the first connection.
	c1, err := l1.Accept()
	if err != nil {
		t.Fatal(err)
	}

	// Wait until Conn reports connected.
	waitConnected(t, conn, true, 2*time.Second)

	// Close the first connection and listener, remove the socket file.
	c1.Close()
	l1.Close()
	os.Remove(sockPath)

	// Wait until Conn reports disconnected.
	waitConnected(t, conn, false, 2*time.Second)

	// Start a second listener at the same path before the first backoff expires.
	l2, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	go func() { l2.Accept() }() //nolint:errcheck

	// Conn should reconnect automatically.
	waitConnected(t, conn, true, 3*time.Second)
}

// waitConnected polls conn.IsConnected() until it equals want or deadline passes.
func waitConnected(t *testing.T, conn *Conn, want bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn.IsConnected() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("IsConnected() did not become %v within %v", want, timeout)
}

// TestUnitConnCloseUnblocks verifies that Close() causes a goroutine blocked
// on Frames() to unblock via context cancellation (not a hard close of the channel).
func TestUnitConnCloseUnblocks(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	ctx := context.Background()
	conn := Dial(ctx, sockPath)

	// Accept so readLoop is running.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		l.Accept() //nolint:errcheck
	}()

	waitConnected(t, conn, true, 2*time.Second)
	wg.Wait()

	done := make(chan struct{})
	go func() {
		select {
		case <-conn.Frames():
		case <-conn.ctx.Done():
		}
		close(done)
	}()

	conn.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close() did not unblock Frames() consumer within 1s")
	}
}

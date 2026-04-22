//go:build integration

package frrmgmt

import (
	"context"
	"testing"
	"time"
)

// TestIntegrationSessionCreate verifies the full SESSION_REQ/REPLY handshake
// against a live mgmtd_fe.sock and confirms the returned session_id is non-zero.
func TestIntegrationSessionCreate(t *testing.T) {
	sockPath := sockPathFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()
	waitConnected(t, conn, true, 2*time.Second)

	d := NewDispatcher(conn.Frames())
	sess, err := New(ctx, conn, d, "routing-mcp-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer sess.Close(context.Background())

	if sess.ID() == 0 {
		t.Fatal("session_id is 0; expected non-zero value assigned by mgmtd")
	}
	t.Logf("session_id = %d", sess.ID())
}

// TestIntegrationSessionClose verifies that Close sends a valid close request
// without error against a live mgmtd_fe.sock.
func TestIntegrationSessionClose(t *testing.T) {
	sockPath := sockPathFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()
	waitConnected(t, conn, true, 2*time.Second)

	d := NewDispatcher(conn.Frames())
	sess, err := New(ctx, conn, d, "routing-mcp-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := sess.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

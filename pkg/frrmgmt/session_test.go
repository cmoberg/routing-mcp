package frrmgmt

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// serveFakeSession accepts one connection, reads the SESSION_REQ frame, and
// sends back a SESSION_REPLY with the given sessionID and created flag.
func serveFakeSession(t *testing.T, l net.Listener, sessionID uint64, created uint8) {
	t.Helper()
	conn, err := l.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	payload, err := ReadFrame(conn)
	if err != nil {
		t.Errorf("fake server: ReadFrame: %v", err)
		return
	}

	var req SessionReqFixed
	if err := binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req); err != nil {
		t.Errorf("fake server: decode SESSION_REQ: %v", err)
		return
	}

	reply := SessionReplyFixed{
		MsgHeader: MsgHeader{
			Code:    CodeSessionReply,
			ReqID:   req.ReqID,
			ReferID: sessionID,
		},
		Created: created,
	}
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, reply) //nolint:errcheck
	if err := WriteFrame(conn, buf.Bytes()); err != nil {
		t.Errorf("fake server: WriteFrame: %v", err)
	}
}

// TestUnitSessionNew verifies that New completes the SESSION_REQ/REPLY handshake
// and returns a Session with the server-assigned session_id.
func TestUnitSessionNew(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "mgmtd_fe.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	const wantID = uint64(0xdeadbeef)
	go serveFakeSession(t, l, wantID, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()
	waitConnected(t, conn, true, 2*time.Second)

	d := NewDispatcher(conn.Frames())
	sess, err := New(ctx, conn, d, "test-client")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if sess.ID() != wantID {
		t.Errorf("session ID: got %d, want %d", sess.ID(), wantID)
	}
}

// TestUnitSessionNewRejected verifies that New returns ErrSessionRejected when
// mgmtd sends SESSION_REPLY with Created=0.
func TestUnitSessionNewRejected(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "mgmtd_fe.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	go serveFakeSession(t, l, 0, 0) // created=0

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()
	waitConnected(t, conn, true, 2*time.Second)

	d := NewDispatcher(conn.Frames())
	_, err = New(ctx, conn, d, "test-client")
	if !errors.Is(err, ErrSessionRejected) {
		t.Fatalf("expected ErrSessionRejected, got %v", err)
	}
}

// TestUnitSessionNewErrNotConnected verifies that New returns ErrNotConnected
// when the socket is not yet connected.
func TestUnitSessionNewErrNotConnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := Dial(ctx, "/nonexistent/mgmtd_fe.sock")
	defer conn.Close()

	d := NewDispatcher(make(chan []byte))
	_, err := New(ctx, conn, d, "test-client")
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("expected ErrNotConnected, got %v", err)
	}
}

// TestUnitSessionClose verifies that Close sends SESSION_REQ with the correct
// refer_id (the session_id assigned during New).
func TestUnitSessionClose(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "mgmtd_fe.sock")

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	const wantID = uint64(42)
	closedWith := make(chan uint64, 1)

	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		// SESSION_REQ create
		payload, _ := ReadFrame(conn)
		var req SessionReqFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req) //nolint:errcheck

		reply := SessionReplyFixed{
			MsgHeader: MsgHeader{Code: CodeSessionReply, ReqID: req.ReqID, ReferID: wantID},
			Created:   1,
		}
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, reply) //nolint:errcheck
		WriteFrame(conn, buf.Bytes())                   //nolint:errcheck

		// SESSION_REQ close — read refer_id
		payload2, _ := ReadFrame(conn)
		var req2 SessionReqFixed
		binary.Read(bytes.NewReader(payload2), binary.LittleEndian, &req2) //nolint:errcheck
		closedWith <- req2.ReferID
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()
	waitConnected(t, conn, true, 2*time.Second)

	d := NewDispatcher(conn.Frames())
	sess, err := New(ctx, conn, d, "test-client")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := sess.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case got := <-closedWith:
		if got != wantID {
			t.Errorf("close refer_id: got %d, want %d", got, wantID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not receive close frame within 2s")
	}
}

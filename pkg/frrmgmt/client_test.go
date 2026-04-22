package frrmgmt

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortSockPath returns a short socket path that stays within macOS's
// 104-byte SOCK_MAXADDRLEN limit regardless of the test name length.
func shortSockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "frr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// fakeMgmtd is a minimal fake mgmtd server for client unit tests.
// It handles SESSION_REQ → SESSION_REPLY automatically and then calls handler
// for each subsequent frame.
type fakeMgmtd struct {
	t         *testing.T
	l         net.Listener
	sessionID uint64
	// handler is called with the raw payload of each non-session message.
	// It must write a reply frame to conn, or do nothing.
	handler func(conn net.Conn, payload []byte)
}

func newFakeMgmtd(t *testing.T, sockPath string, sessionID uint64) *fakeMgmtd {
	t.Helper()
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	f := &fakeMgmtd{t: t, l: l, sessionID: sessionID}
	return f
}

func (f *fakeMgmtd) run() {
	conn, err := f.l.Accept()
	if err != nil {
		return
	}

	// Handle SESSION_REQ.
	payload, err := ReadFrame(conn)
	if err != nil {
		f.t.Errorf("fakeMgmtd: ReadFrame session: %v", err)
		return
	}
	var req SessionReqFixed
	binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req) //nolint:errcheck
	reply := SessionReplyFixed{
		MsgHeader: MsgHeader{Code: CodeSessionReply, ReqID: req.ReqID, ReferID: f.sessionID},
		Created:   1,
	}
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, reply) //nolint:errcheck
	WriteFrame(conn, buf.Bytes())                   //nolint:errcheck

	// Handle subsequent messages.
	for {
		p, err := ReadFrame(conn)
		if err != nil {
			return
		}
		if f.handler != nil {
			f.handler(conn, p)
		}
	}
}

// sendReply writes a fixed-struct reply frame to conn, echoing reqID.
func sendReply(t *testing.T, conn net.Conn, v any, reqID uint64) {
	t.Helper()
	b := encodeFixed(v)
	binary.LittleEndian.PutUint64(b[16:24], reqID)
	if err := WriteFrame(conn, b); err != nil {
		t.Errorf("sendReply: %v", err)
	}
}

// dialFake sets up a Conn+Dispatcher+Session against the fakeMgmtd.
func dialFake(t *testing.T, sockPath string, sessionID uint64) (*Client, func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn := Dial(ctx, sockPath)
	waitConnected(t, conn, true, 2*time.Second)
	d := NewDispatcher(conn.Frames())
	sess, err := New(ctx, conn, d, "test")
	if err != nil {
		cancel()
		conn.Close()
		t.Fatalf("dialFake: New: %v", err)
	}
	if sess.ID() != sessionID {
		cancel()
		conn.Close()
		t.Fatalf("dialFake: session ID %d, want %d", sess.ID(), sessionID)
	}
	client := NewClient(sess, d, conn)
	cleanup := func() {
		conn.Close()
		cancel()
	}
	return client, cleanup
}

// TestUnitClientGetData verifies GetData sends CodeGetData and assembles
// single-fragment JSON from a TreeData reply.
func TestUnitClientGetData(t *testing.T) {
	sockPath := shortSockPath(t)

	const sessID = uint64(1)
	wantJSON := []byte(`{"metric":1}`)

	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req GetDataFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)

		vsplit, varData := EncodeXpathData("/frr-staticd:lib", wantJSON)
		hdr := TreeDataFixed{
			MsgHeader: MsgHeader{
				Code:    CodeTreeData,
				ReqID:   req.ReqID,
				VSplit:  vsplit,
			},
			More: 0,
		}
		b := append(encodeFixed(hdr), varData...)
		WriteFrame(conn, b) //nolint:errcheck
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	ctx := context.Background()
	got, err := client.GetData(ctx, "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if !bytes.Equal(got, wantJSON) {
		t.Errorf("GetData: got %q, want %q", got, wantJSON)
	}
}

// TestUnitClientGetDataMultiFragment verifies that GetData assembles JSON from
// three TreeData frames (more=1,1,0).
func TestUnitClientGetDataMultiFragment(t *testing.T) {
	sockPath := shortSockPath(t)

	const sessID = uint64(2)
	chunks := [][]byte{[]byte(`chunk1`), []byte(`chunk2`), []byte(`chunk3`)}

	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req GetDataFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)

		for i, chunk := range chunks {
			more := uint8(1)
			if i == len(chunks)-1 {
				more = 0
			}
			vsplit, varData := EncodeXpathData("/frr-staticd:lib", chunk)
			hdr := TreeDataFixed{
				MsgHeader: MsgHeader{Code: CodeTreeData, ReqID: req.ReqID, VSplit: vsplit},
				More:      more,
			}
			b := append(encodeFixed(hdr), varData...)
			WriteFrame(conn, b) //nolint:errcheck
		}
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	got, err := client.GetData(context.Background(), "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	want := []byte("chunk1chunk2chunk3")
	if !bytes.Equal(got, want) {
		t.Errorf("GetData assembled: got %q, want %q", got, want)
	}
}

// TestUnitClientLock verifies Lock sends CodeLock with lock=1 and handles
// the LockReply.
func TestUnitClientLock(t *testing.T) {
	sockPath := shortSockPath(t)
	const sessID = uint64(3)

	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req LockFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)
		if req.Code != CodeLock {
			t.Errorf("expected CodeLock, got %d", req.Code)
		}
		if req.Lock != 1 {
			t.Errorf("Lock field: got %d, want 1", req.Lock)
		}
		reply := LockReplyFixed{
			MsgHeader: MsgHeader{Code: CodeLockReply, ReqID: req.ReqID},
		}
		sendReply(t, conn, reply, req.ReqID)
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	if err := client.Lock(context.Background(), DatastoreCandidate); err != nil {
		t.Fatalf("Lock: %v", err)
	}
}

// TestUnitClientEdit verifies Edit sends the correct xpath+data and returns
// the EditResult from the EditReply.
func TestUnitClientEdit(t *testing.T) {
	sockPath := shortSockPath(t)
	const sessID = uint64(4)

	wantXPath := "/frr-staticd:lib/route-list[prefix='10.0.0.0/8']"
	wantData := []byte(`{"metric":1}`)

	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req EditFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)

		gotXPath, gotData, err := DecodeXpathData(req.VSplit, payload[32:])
		if err != nil {
			t.Errorf("DecodeXpathData: %v", err)
		}
		if gotXPath != wantXPath {
			t.Errorf("xpath: got %q, want %q", gotXPath, wantXPath)
		}
		if !bytes.Equal(gotData, wantData) {
			t.Errorf("data: got %q, want %q", gotData, wantData)
		}

		canonXPath := wantXPath + "\x00"
		vsplit, _ := EncodeXpathData(wantXPath, nil)
		reply := EditReplyFixed{
			MsgHeader: MsgHeader{Code: CodeEditReply, ReqID: req.ReqID, VSplit: vsplit},
			Changed:   1,
			Created:   1,
		}
		b := append(encodeFixed(reply), []byte(canonXPath)...)
		WriteFrame(conn, b) //nolint:errcheck
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	result, err := client.Edit(context.Background(), wantXPath, EditOpMerge, wantData)
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if !result.Changed || !result.Created {
		t.Errorf("EditResult: Changed=%v Created=%v, want both true", result.Changed, result.Created)
	}
	if result.XPath != wantXPath {
		t.Errorf("EditResult.XPath: got %q, want %q", result.XPath, wantXPath)
	}
}

// TestUnitClientCommit verifies Commit sends the correct action and unlock flag.
func TestUnitClientCommit(t *testing.T) {
	sockPath := shortSockPath(t)
	const sessID = uint64(5)

	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req CommitFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)
		if req.Action != CommitApply {
			t.Errorf("Action: got %d, want CommitApply", req.Action)
		}
		if req.Unlock != 1 {
			t.Errorf("Unlock: got %d, want 1", req.Unlock)
		}
		reply := CommitReplyFixed{
			MsgHeader: MsgHeader{Code: CodeCommitReply, ReqID: req.ReqID},
		}
		sendReply(t, conn, reply, req.ReqID)
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	if err := client.Commit(context.Background(), CommitApply, true); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

// TestUnitClientGetDataError verifies GetData propagates an ErrorFixed reply
// as a non-nil Go error.
func TestUnitClientGetDataError(t *testing.T) {
	sockPath := shortSockPath(t)
	const sessID = uint64(6)

	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req GetDataFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)
		// Reply with CodeError instead of CodeTreeData.
		errReply := ErrorFixed{
			MsgHeader: MsgHeader{Code: CodeError, ReqID: req.ReqID},
			Error:     -1,
		}
		sendReply(t, conn, errReply, req.ReqID)
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	_, err := client.GetData(context.Background(), "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
	if err == nil {
		t.Fatal("expected error from CodeError reply, got nil")
	}
}

// TestUnitClientSubscribe verifies Subscribe sends NOTIFY_SELECT and forwards
// incoming CodeNotify frames as Notifications.
func TestUnitClientSubscribe(t *testing.T) {
	sockPath := shortSockPath(t)
	const sessID = uint64(7)

	notifSent := make(chan struct{})
	f := newFakeMgmtd(t, sockPath, sessID)
	f.handler = func(conn net.Conn, payload []byte) {
		var req NotifySelectFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req)
		if req.Code != CodeNotifySelect {
			t.Errorf("expected CodeNotifySelect, got %d", req.Code)
			return
		}
		// Send a NOTIFY_DATA frame (no reqID correlation needed).
		vsplit, varData := EncodeXpathData("/frr-staticd:lib", []byte(`{"x":1}`))
		hdr := NotifyDataFixed{
			MsgHeader: MsgHeader{Code: CodeNotify, VSplit: vsplit},
			Op:        NotifyOpNotification,
		}
		b := append(encodeFixed(hdr), varData...)
		WriteFrame(conn, b) //nolint:errcheck
		close(notifSent)
	}
	go f.run()

	client, cleanup := dialFake(t, sockPath, sessID)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.Subscribe(ctx, []string{"/frr-staticd:lib"}, true)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	select {
	case n := <-ch:
		if n.XPath != "/frr-staticd:lib" {
			t.Errorf("Notification.XPath: got %q, want /frr-staticd:lib", n.XPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

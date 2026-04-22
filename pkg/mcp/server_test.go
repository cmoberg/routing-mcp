package mcp

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmoberg/routing-mcp/pkg/frrmgmt"
	"github.com/mark3labs/mcp-go/mcp"
)

// ─── Fake mgmtd helpers ───────────────────────────────────────────────────────

// shortSock creates a temp socket path short enough for macOS's 104-byte limit.
func shortSock(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rmcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

// serveFakeSessionOnce accepts one connection, replies to SESSION_REQ with the
// given sessionID, and then calls handler for every subsequent frame.
func serveFakeSessionOnce(t *testing.T, l net.Listener, sessionID uint64,
	handler func(conn net.Conn, payload []byte),
) {
	t.Helper()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		payload, err := frrmgmt.ReadFrame(conn)
		if err != nil {
			return
		}
		var req frrmgmt.SessionReqFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req) //nolint:errcheck

		reply := frrmgmt.SessionReplyFixed{
			MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeSessionReply, ReqID: req.ReqID, ReferID: sessionID},
			Created:   1,
		}
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, reply) //nolint:errcheck
		frrmgmt.WriteFrame(conn, buf.Bytes())          //nolint:errcheck

		for {
			p, err := frrmgmt.ReadFrame(conn)
			if err != nil {
				return
			}
			if handler != nil {
				handler(conn, p)
			}
		}
	}()
}

// encodeFixed is a test-local helper (avoids importing the unexported one).
func encodeFixedTest(v any) []byte {
	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, v) //nolint:errcheck
	return buf.Bytes()
}

// newTestServer dials a fake mgmtd, creates the full frrmgmt stack, and wraps
// it in an mcp.Server. handler is called for each non-session mgmtd frame.
func newTestServer(t *testing.T, sessionID uint64,
	handler func(conn net.Conn, payload []byte),
) (*Server, context.CancelFunc) {
	t.Helper()
	sockPath := shortSock(t)

	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	serveFakeSessionOnce(t, l, sessionID, handler)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	conn := frrmgmt.Dial(ctx, sockPath)
	t.Cleanup(func() { conn.Close() })

	// wait for connection
	deadline := time.Now().Add(2 * time.Second)
	for !conn.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !conn.IsConnected() {
		cancel()
		t.Fatal("timed out waiting for connection")
	}

	d := frrmgmt.NewDispatcher(conn.Frames())
	sess, err := frrmgmt.New(ctx, conn, d, "test")
	if err != nil {
		cancel()
		t.Fatalf("session: %v", err)
	}
	client := frrmgmt.NewClient(sess, d, conn)

	srv, err := NewServer(ctx, client, "")
	if err != nil {
		cancel()
		t.Fatalf("NewServer: %v", err)
	}
	return srv, cancel
}

// callTool invokes a tool handler directly using the internal mcp.CallToolRequest type.
func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error),
	args map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	return result
}

// resultText extracts the text from the first content item.
func resultText(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r == nil || len(r.Content) == 0 {
		t.Fatal("empty tool result")
	}
	text, ok := r.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}
	return text.Text
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestMCPGetConfig verifies get_config issues GetData and returns the JSON body.
func TestMCPGetConfig(t *testing.T) {
	wantJSON := []byte(`{"metric":1}`)

	srv, cancel := newTestServer(t, 1, func(conn net.Conn, payload []byte) {
		code := binary.LittleEndian.Uint16(payload[0:2])
		reqID := binary.LittleEndian.Uint64(payload[16:24])
		if code != frrmgmt.CodeGetData {
			return
		}
		vsplit, varData := frrmgmt.EncodeXpathData("/frr-staticd:lib", wantJSON)
		hdr := frrmgmt.TreeDataFixed{
			MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeTreeData, ReqID: reqID, VSplit: vsplit},
		}
		frrmgmt.WriteFrame(conn, append(encodeFixedTest(hdr), varData...)) //nolint:errcheck
	})
	defer cancel()

	result := callTool(t, srv.handleGetConfig, map[string]any{"xpath": "/frr-staticd:lib"})
	if result.IsError {
		t.Fatalf("tool returned error: %s", resultText(t, result))
	}
	if got := resultText(t, result); got != string(wantJSON) {
		t.Errorf("get_config: got %q, want %q", got, wantJSON)
	}
}

// TestMCPGetState verifies get_state uses the Operational datastore.
func TestMCPGetState(t *testing.T) {
	srv, cancel := newTestServer(t, 2, func(conn net.Conn, payload []byte) {
		code := binary.LittleEndian.Uint16(payload[0:2])
		reqID := binary.LittleEndian.Uint64(payload[16:24])
		if code != frrmgmt.CodeGetData {
			return // ignore NOTIFY_SELECT and other frames
		}
		var req frrmgmt.GetDataFixed
		binary.Read(bytes.NewReader(payload), binary.LittleEndian, &req) //nolint:errcheck

		if req.Datastore != frrmgmt.DatastoreOperational {
			t.Errorf("get_state: datastore %d, want Operational (%d)",
				req.Datastore, frrmgmt.DatastoreOperational)
		}

		vsplit, varData := frrmgmt.EncodeXpathData("/frr-zebra:zebra", []byte(`{}`))
		hdr := frrmgmt.TreeDataFixed{
			MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeTreeData, ReqID: reqID, VSplit: vsplit},
		}
		frrmgmt.WriteFrame(conn, append(encodeFixedTest(hdr), varData...)) //nolint:errcheck
	})
	defer cancel()

	result := callTool(t, srv.handleGetState, map[string]any{"xpath": "/frr-zebra:zebra"})
	if result.IsError {
		t.Fatalf("get_state returned error: %s", resultText(t, result))
	}
}

// TestMCPSetConfig verifies set_config sends Lock→Edit→Commit and returns ok.
func TestMCPSetConfig(t *testing.T) {
	srv, cancel := newTestServer(t, 3, func(conn net.Conn, payload []byte) {
		code := binary.LittleEndian.Uint16(payload[0:2])
		reqID := binary.LittleEndian.Uint64(payload[16:24])

		switch code {
		case frrmgmt.CodeLock:
			reply := frrmgmt.LockReplyFixed{
				MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeLockReply, ReqID: reqID},
			}
			frrmgmt.WriteFrame(conn, encodeFixedTest(reply)) //nolint:errcheck

		case frrmgmt.CodeEdit:
			vsplit, varData := frrmgmt.EncodeXpathData("/frr-staticd:lib/route-list", nil)
			reply := frrmgmt.EditReplyFixed{
				MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeEditReply, ReqID: reqID, VSplit: vsplit},
				Changed:   1,
			}
			frrmgmt.WriteFrame(conn, append(encodeFixedTest(reply), varData...)) //nolint:errcheck

		case frrmgmt.CodeCommit:
			reply := frrmgmt.CommitReplyFixed{
				MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeCommitReply, ReqID: reqID},
			}
			frrmgmt.WriteFrame(conn, encodeFixedTest(reply)) //nolint:errcheck
		}
	})
	defer cancel()

	result := callTool(t, srv.handleSetConfig, map[string]any{
		"xpath": "/frr-staticd:lib/route-list",
		"data":  `{"metric":1}`,
	})
	if result.IsError {
		t.Fatalf("set_config returned error: %s", resultText(t, result))
	}
	if !strings.HasPrefix(resultText(t, result), "ok:") {
		t.Errorf("set_config: expected ok: prefix, got %q", resultText(t, result))
	}
}

// TestMCPGetNotificationsEmpty verifies get_notifications returns an empty
// JSON array when no notifications have arrived.
func TestMCPGetNotificationsEmpty(t *testing.T) {
	srv, cancel := newTestServer(t, 4, func(conn net.Conn, payload []byte) {
		// NOTIFY_SELECT comes in but we never send any notifications.
	})
	defer cancel()

	result := callTool(t, srv.handleGetNotifications, map[string]any{"max_count": float64(10)})
	if result.IsError {
		t.Fatalf("get_notifications error: %s", resultText(t, result))
	}
	text := resultText(t, result)
	var got []notifJSON
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("unmarshal: %v — raw: %s", err, text)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 notifications, got %d", len(got))
	}
}

// TestMCPGetNotificationsBuffered verifies that notifications arriving on the
// mgmtd connection are buffered and returned by get_notifications.
func TestMCPGetNotificationsBuffered(t *testing.T) {
	notifSent := make(chan struct{})

	srv, cancel := newTestServer(t, 5, func(conn net.Conn, payload []byte) {
		code := binary.LittleEndian.Uint16(payload[0:2])
		if code != frrmgmt.CodeNotifySelect {
			return
		}
		// Push one NOTIFY_DATA frame (no reqID correlation needed).
		vsplit, varData := frrmgmt.EncodeXpathData("/frr-staticd:lib", []byte(`{"x":1}`))
		hdr := frrmgmt.NotifyDataFixed{
			MsgHeader: frrmgmt.MsgHeader{Code: frrmgmt.CodeNotify, VSplit: vsplit},
			Op:        frrmgmt.NotifyOpNotification,
		}
		frrmgmt.WriteFrame(conn, append(encodeFixedTest(hdr), varData...)) //nolint:errcheck
		close(notifSent)
	})
	defer cancel()

	// Wait for the notification to be buffered.
	select {
	case <-notifSent:
	case <-time.After(2 * time.Second):
		t.Fatal("notification not sent by fake server within 2s")
	}
	time.Sleep(50 * time.Millisecond) // let collectNotifications append

	result := callTool(t, srv.handleGetNotifications, map[string]any{"max_count": float64(10)})
	if result.IsError {
		t.Fatalf("get_notifications error: %s", resultText(t, result))
	}
	var got []notifJSON
	if err := json.Unmarshal([]byte(resultText(t, result)), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one notification, got none")
	}
	if got[0].XPath != "/frr-staticd:lib" {
		t.Errorf("notification xpath: got %q, want /frr-staticd:lib", got[0].XPath)
	}
}

// TestMCPYangIndex verifies the yang index resource lists .yang files.
func TestMCPYangIndex(t *testing.T) {
	dir := t.TempDir()
	// Create fake .yang files.
	for _, name := range []string{"frr-staticd.yang", "frr-zebra.yang", "README.txt"} {
		os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644) //nolint:errcheck
	}

	srv := &Server{yangDir: dir}

	req := mcp.ReadResourceRequest{}
	req.Params.URI = "frr://yang/index"
	contents, err := srv.handleYangIndex(context.Background(), req)
	if err != nil {
		t.Fatalf("handleYangIndex: %v", err)
	}
	if len(contents) == 0 {
		t.Fatal("no contents returned")
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", contents[0])
	}
	var modules []string
	if err := json.Unmarshal([]byte(text.Text), &modules); err != nil {
		t.Fatalf("unmarshal: %v — %s", err, text.Text)
	}
	want := map[string]bool{"frr-staticd": true, "frr-zebra": true}
	for _, m := range modules {
		delete(want, m)
	}
	if len(want) > 0 {
		t.Errorf("missing modules: %v", want)
	}
	for _, m := range modules {
		if m == "README" {
			t.Error("non-.yang file should not appear in index")
		}
	}
}

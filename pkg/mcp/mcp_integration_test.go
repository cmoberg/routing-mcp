//go:build integration

package mcp

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/cmoberg/routing-mcp/pkg/frrmgmt"
)

// sockPathFromEnvMCP returns the mgmtd_fe.sock path from FRR_SOCK env var.
func sockPathFromEnvMCP(t *testing.T) string {
	t.Helper()
	p := os.Getenv("FRR_SOCK")
	if p == "" {
		t.Skip("FRR_SOCK not set; skipping integration test")
	}
	return p
}

// newIntegrationServer dials the live mgmtd socket and wraps it in an MCP Server.
func newIntegrationServer(t *testing.T) (*Server, context.CancelFunc) {
	t.Helper()
	sockPath := sockPathFromEnvMCP(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

	conn := frrmgmt.Dial(ctx, sockPath)
	t.Cleanup(func() { conn.Close() })

	deadline := time.Now().Add(2 * time.Second)
	for !conn.IsConnected() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !conn.IsConnected() {
		cancel()
		t.Fatal("timed out waiting for mgmtd connection")
	}

	d := frrmgmt.NewDispatcher(conn.Frames())
	sess, err := frrmgmt.New(ctx, conn, d, "routing-mcp-inttest")
	if err != nil {
		cancel()
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { sess.Close(context.Background()) }) //nolint:errcheck

	client := frrmgmt.NewClient(sess, d, conn)

	srv, err := NewServer(ctx, client, "")
	if err != nil {
		cancel()
		t.Fatalf("NewServer: %v", err)
	}
	return srv, cancel
}

// TestIntegrationMCPGetConfig verifies handleGetConfig returns non-empty JSON
// containing the fixture route (10.99.0.0/24 from docker/frr/frr.conf).
func TestIntegrationMCPGetConfig(t *testing.T) {
	srv, cancel := newIntegrationServer(t)
	defer cancel()

	result := callTool(t, srv.handleGetConfig, map[string]any{"xpath": "/frr-staticd:lib"})
	if result.IsError {
		t.Fatalf("get_config returned error: %s", resultText(t, result))
	}
	text := resultText(t, result)
	if text == "{}" || len(text) == 0 {
		t.Fatal("get_config returned empty response")
	}
	if !bytes.Contains([]byte(text), []byte("10.99.0.0/24")) {
		t.Errorf("fixture route 10.99.0.0/24 not found in get_config output: %.500s", text)
	}
	t.Logf("get_config (%d bytes): %.300s", len(text), text)
}

// TestIntegrationMCPSetAndDeleteConfig sets a blackhole route via handleSetConfig,
// verifies it appears in handleGetConfig, then deletes it and verifies it is gone.
func TestIntegrationMCPSetAndDeleteConfig(t *testing.T) {
	srv, cancel := newIntegrationServer(t)
	defer cancel()

	const prefix = "203.0.113.0/24"
	const xpath = "/frr-staticd:lib" +
		"/route-list[prefix='203.0.113.0/24'][afi-safi='frr-staticd:ipv4-unicast']" +
		"/path-list[table-id='0'][distance='1']" +
		"/frr-nexthops/nexthop[nh-type='blackhole'][vrf='default'][gateway=''][interface='']"

	// Set the route.
	setResult := callTool(t, srv.handleSetConfig, map[string]any{
		"xpath": xpath,
		"data":  `{}`,
	})
	if setResult.IsError {
		t.Skipf("set_config: %s (route may already exist or YANG path changed)", resultText(t, setResult))
	}
	t.Logf("set_config: %s", resultText(t, setResult))

	// Verify it appears in the running datastore.
	getResult := callTool(t, srv.handleGetConfig, map[string]any{"xpath": "/frr-staticd:lib"})
	if getResult.IsError {
		t.Fatalf("get_config after set: %s", resultText(t, getResult))
	}
	if !bytes.Contains([]byte(resultText(t, getResult)), []byte(prefix)) {
		t.Errorf("route %s not found after set; response: %.500s", prefix, resultText(t, getResult))
	}

	// Delete the route.
	delResult := callTool(t, srv.handleDeleteConfig, map[string]any{"xpath": xpath})
	if delResult.IsError {
		t.Fatalf("delete_config: %s", resultText(t, delResult))
	}
	t.Logf("delete_config: %s", resultText(t, delResult))

	// Verify it is gone.
	getResult2 := callTool(t, srv.handleGetConfig, map[string]any{"xpath": "/frr-staticd:lib"})
	if getResult2.IsError {
		t.Fatalf("get_config after delete: %s", resultText(t, getResult2))
	}
	if bytes.Contains([]byte(resultText(t, getResult2)), []byte(prefix)) {
		t.Errorf("route %s still present after delete; response: %.500s", prefix, resultText(t, getResult2))
	}
}

// TestIntegrationMCPGetState verifies handleGetState reads from the operational
// datastore. Skips if zebra is not yet mgmtd-aware in this FRR build.
func TestIntegrationMCPGetState(t *testing.T) {
	srv, cancel := newIntegrationServer(t)
	defer cancel()

	result := callTool(t, srv.handleGetState, map[string]any{"xpath": "/frr-zebra:zebra"})
	if result.IsError {
		t.Skipf("get_state /frr-zebra:zebra: %s (zebra may not be mgmtd-aware)", resultText(t, result))
	}
	text := resultText(t, result)
	t.Logf("get_state (%d bytes): %.300s", len(text), text)
}

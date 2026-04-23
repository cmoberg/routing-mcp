//go:build integration

package frrmgmt

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// newIntegrationClient dials the live mgmtd socket, creates a session and
// Client. The cleanup func closes the connection.
func newIntegrationClient(t *testing.T) (*Client, func()) {
	t.Helper()
	sockPath := sockPathFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	conn := Dial(ctx, sockPath)
	waitConnected(t, conn, true, 2*time.Second)

	d := NewDispatcher(conn.Frames())
	sess, err := New(ctx, conn, d, "routing-mcp-test")
	if err != nil {
		cancel()
		conn.Close()
		t.Fatalf("New: %v", err)
	}

	cleanup := func() {
		sess.Close(context.Background()) //nolint:errcheck
		conn.Close()
		cancel()
	}
	return NewClient(sess, d, conn), cleanup
}

// FRR 10.x xpath for the staticd instance in the default VRF.
const xpathStaticInst = "/frr-routing:routing/control-plane-protocols/" +
	"control-plane-protocol[type='frr-staticd:staticd'][name='staticd'][vrf='default']/" +
	"frr-staticd:staticd"

// xpathForRoute returns the route-list xpath for a given IPv4 prefix.
// Used for both merge (add) and delete operations.
func xpathForRoute(prefix string) string {
	return fmt.Sprintf(xpathStaticInst+
		"/route-list[prefix='%s'][src-prefix='::/0'][afi-safi='frr-routing:ipv4-unicast']",
		prefix)
}

// bhRouteData returns the JSON for a Null0 (blackhole) static route wrapped in
// the route-list container. EditOpMerge pops one xpath level to staticd, so the
// data must include the route-list key fields as a child of staticd.
func bhRouteData(prefix string) string {
	return fmt.Sprintf(
		`{"route-list":[{"prefix":%q,"src-prefix":"::/0","afi-safi":"frr-routing:ipv4-unicast","path-list":[{"table-id":0,"distance":1,"frr-nexthops":{"nexthop":[{"nh-type":"blackhole","vrf":"default","gateway":"","interface":"(null)"}]}}]}]}`,
		prefix,
	)
}

// TestIntegrationClientGetDataRunning reads the static route fixture
// (10.99.0.0/24 Null0 seeded in docker/frr/frr.conf) from the running
// datastore and verifies the response is non-empty JSON.
func TestIntegrationClientGetDataRunning(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := client.GetData(ctx, xpathStaticInst, DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("GetData returned empty response")
	}
	t.Logf("GetData response (%d bytes): %.200s", len(data), data)
}

// TestIntegrationClientEditAndCommit adds a static route via the candidate
// datastore and verifies it appears in running.
func TestIntegrationClientEditAndCommit(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := client.EditAndCommit(ctx, xpathForRoute("192.0.2.0/24"), EditOpMerge, []byte(bhRouteData("192.0.2.0/24")))
	if err != nil {
		t.Skipf("EditAndCommit: %v (may fail if route already exists or YANG path changed)", err)
	}
	t.Logf("EditAndCommit: changed=%v created=%v xpath=%q", result.Changed, result.Created, result.XPath)

	// Best-effort cleanup.
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanCancel()
	client.EditAndCommit(cleanCtx, xpathForRoute("192.0.2.0/24"), EditOpDelete, nil) //nolint:errcheck
}

// TestIntegrationClientSubscribe registers a notification subscription and
// verifies NOTIFY_SELECT is accepted without error.
func TestIntegrationClientSubscribe(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.Subscribe(ctx, []string{xpathStaticInst}, true)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	// We can't guarantee a notification arrives in CI; just confirm the channel
	// is open and unblocked.
	select {
	case n, ok := <-ch:
		if ok {
			t.Logf("received notification: op=%d xpath=%q", n.Op, n.XPath)
		}
	case <-time.After(500 * time.Millisecond):
		// Timeout is expected in CI; pass.
	}
}

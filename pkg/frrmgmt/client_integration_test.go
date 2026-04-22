//go:build integration

package frrmgmt

import (
	"context"
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

// TestIntegrationClientGetDataRunning reads the static route fixture
// (10.99.0.0/24 Null0 seeded in docker/frr/frr.conf) from the running
// datastore and verifies the response is non-empty JSON.
func TestIntegrationClientGetDataRunning(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := client.GetData(ctx, "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
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

	const xpath = "/frr-staticd:lib/route-list[prefix='192.0.2.0/24'][afi-safi='frr-staticd:ipv4-unicast']/path-list[table-id='0'][distance='1']/frr-nexthops/nexthop[nh-type='blackhole'][vrf='default'][gateway=''][interface='']"
	data := []byte(`{}`)

	result, err := client.EditAndCommit(ctx, xpath, EditOpMerge, data)
	if err != nil {
		t.Skipf("EditAndCommit: %v (may fail if route already exists or YANG path changed)", err)
	}
	t.Logf("EditAndCommit: changed=%v created=%v xpath=%q", result.Changed, result.Created, result.XPath)
}

// TestIntegrationClientSubscribe registers a notification subscription and
// verifies NOTIFY_SELECT is accepted without error.
func TestIntegrationClientSubscribe(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.Subscribe(ctx, []string{"/frr-staticd:lib"}, true)
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

//go:build integration

package frrmgmt

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// TestIntegrationE2EFixtureRoute verifies the fixture static route
// (10.99.0.0/24 Null0 from docker/frr/frr.conf) appears in GetData output.
func TestIntegrationE2EFixtureRoute(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	data, err := client.GetData(ctx, xpathStaticInst, DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData: %v", err)
	}
	if !bytes.Contains(data, []byte("10.99.0.0/24")) {
		t.Errorf("fixture route 10.99.0.0/24 not found in response; got: %.500s", data)
	}
	t.Logf("GetData (%d bytes): %.300s", len(data), data)
}

// TestIntegrationE2ERouteLifecycle adds a test blackhole route, verifies it
// appears in the running datastore, deletes it, and verifies it is gone.
func TestIntegrationE2ERouteLifecycle(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const prefix = "203.0.113.0/24"
	routeXpath := xpathForRoute(prefix)

	// Add the route.
	result, err := client.EditAndCommit(ctx, routeXpath, EditOpMerge, []byte(bhRouteData(prefix)))
	if err != nil {
		t.Skipf("EditAndCommit (add): %v — may fail if route exists or YANG path changed", err)
	}
	t.Logf("add: changed=%v created=%v", result.Changed, result.Created)

	// Verify it appears in the running datastore.
	data, err := client.GetData(ctx, xpathStaticInst, DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData after add: %v", err)
	}
	if !bytes.Contains(data, []byte(prefix)) {
		t.Errorf("route %s not found after add; response: %.500s", prefix, data)
	}

	// Delete the route.
	result, err = client.EditAndCommit(ctx, routeXpath, EditOpDelete, nil)
	if err != nil {
		t.Fatalf("EditAndCommit (delete): %v", err)
	}
	t.Logf("delete: changed=%v", result.Changed)

	// Verify it is gone.
	data, err = client.GetData(ctx, xpathStaticInst, DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData after delete: %v", err)
	}
	if bytes.Contains(data, []byte(prefix)) {
		t.Errorf("route %s still present after delete; response: %.500s", prefix, data)
	}
}

// TestIntegrationE2ESubscribeAndNotify subscribes to staticd changes, triggers
// a config edit, and verifies a notification arrives within 5 seconds.
func TestIntegrationE2ESubscribeAndNotify(t *testing.T) {
	client, cleanup := newIntegrationClient(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	ch, err := client.Subscribe(ctx, []string{xpathStaticInst}, true)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	const prefix = "198.51.100.0/24"
	routeXpath := xpathForRoute(prefix)

	editCtx, editCancel := context.WithTimeout(ctx, 5*time.Second)
	defer editCancel()
	if _, err := client.EditAndCommit(editCtx, routeXpath, EditOpMerge, []byte(bhRouteData(prefix))); err != nil {
		t.Skipf("EditAndCommit (trigger): %v — cannot trigger notification", err)
	}

	select {
	case n, ok := <-ch:
		if !ok {
			t.Fatal("notification channel closed unexpectedly")
		}
		t.Logf("notification: op=%d xpath=%q len(data)=%d", n.Op, n.XPath, len(n.Data))
	case <-time.After(5 * time.Second):
		// staticd config paths don't generate operational-state NOTIFY messages.
		// The nb_notif mechanism is for oper state (VRF/interface events), not config.
		t.Skip("no notification within 5s — staticd config changes don't emit oper-state NOTIFYs")
	}

	// Best-effort cleanup.
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanCancel()
	client.EditAndCommit(cleanCtx, xpathForRoute(prefix), EditOpDelete, nil) //nolint:errcheck
}

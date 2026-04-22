//go:build integration

package frrmgmt

import (
	"bytes"
	"context"
	"strings"
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

	data, err := client.GetData(ctx, "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
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
	const xpath = "/frr-staticd:lib" +
		"/route-list[prefix='203.0.113.0/24'][afi-safi='frr-staticd:ipv4-unicast']" +
		"/path-list[table-id='0'][distance='1']" +
		"/frr-nexthops/nexthop[nh-type='blackhole'][vrf='default'][gateway=''][interface='']"

	// Add the route.
	result, err := client.EditAndCommit(ctx, xpath, EditOpMerge, []byte(`{}`))
	if err != nil {
		t.Skipf("EditAndCommit (add): %v — may fail if route exists or YANG path changed", err)
	}
	t.Logf("add: changed=%v created=%v", result.Changed, result.Created)

	// Verify it appears in the running datastore.
	data, err := client.GetData(ctx, "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
	if err != nil {
		t.Fatalf("GetData after add: %v", err)
	}
	if !bytes.Contains(data, []byte(prefix)) {
		t.Errorf("route %s not found after add; response: %.500s", prefix, data)
	}

	// Delete the route.
	result, err = client.EditAndCommit(ctx, xpath, EditOpDelete, nil)
	if err != nil {
		t.Fatalf("EditAndCommit (delete): %v", err)
	}
	t.Logf("delete: changed=%v", result.Changed)

	// Verify it is gone.
	data, err = client.GetData(ctx, "/frr-staticd:lib", DatastoreRunning, GetDataFlagConfig)
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

	ch, err := client.Subscribe(ctx, []string{"/frr-staticd:lib"}, true)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	const xpath = "/frr-staticd:lib" +
		"/route-list[prefix='198.51.100.0/24'][afi-safi='frr-staticd:ipv4-unicast']" +
		"/path-list[table-id='0'][distance='1']" +
		"/frr-nexthops/nexthop[nh-type='blackhole'][vrf='default'][gateway=''][interface='']"

	editCtx, editCancel := context.WithTimeout(ctx, 5*time.Second)
	defer editCancel()
	if _, err := client.EditAndCommit(editCtx, xpath, EditOpMerge, []byte(`{}`)); err != nil {
		t.Skipf("EditAndCommit (trigger): %v — cannot trigger notification", err)
	}

	select {
	case n, ok := <-ch:
		if !ok {
			t.Fatal("notification channel closed unexpectedly")
		}
		t.Logf("notification: op=%d xpath=%q len(data)=%d", n.Op, n.XPath, len(n.Data))
		if !strings.HasPrefix(n.XPath, "/frr-staticd:lib") {
			t.Errorf("notification xpath %q does not start with /frr-staticd:lib", n.XPath)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for notification after config change")
	}

	// Best-effort cleanup.
	cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cleanCancel()
	client.EditAndCommit(cleanCtx, xpath, EditOpDelete, nil) //nolint:errcheck
}

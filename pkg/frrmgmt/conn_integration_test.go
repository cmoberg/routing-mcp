//go:build integration

package frrmgmt

import (
	"context"
	"os"
	"testing"
	"time"
)

// sockPathFromEnv returns the mgmtd_fe.sock path from FRR_SOCK env var.
func sockPathFromEnv(t *testing.T) string {
	t.Helper()
	p := os.Getenv("FRR_SOCK")
	if p == "" {
		t.Skip("FRR_SOCK not set; skipping integration test")
	}
	return p
}

// TestIntegrationConnDial verifies that Dial establishes a connection to the
// live mgmtd_fe.sock within 2 seconds.
func TestIntegrationConnDial(t *testing.T) {
	sockPath := sockPathFromEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := Dial(ctx, sockPath)
	defer conn.Close()

	waitConnected(t, conn, true, 2*time.Second)
	t.Logf("connected to %s", sockPath)
}

// TestIntegrationConnReconnect requires manual container restart and is skipped
// in automated runs. Run manually with: make run-test T=TestIntegrationConnReconnect
func TestIntegrationConnReconnect(t *testing.T) {
	t.Skip("manual test: stop and restart the frr container while this runs")
}

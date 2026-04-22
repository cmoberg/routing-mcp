package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cmoberg/routing-mcp/pkg/frrmgmt"
	rmcp "github.com/cmoberg/routing-mcp/pkg/mcp"
)

func main() {
	sockPath := flag.String("socket", "/run/frr/mgmtd_fe.sock", "path to mgmtd_fe.sock")
	transport := flag.String("transport", "stdio", "MCP transport: stdio or http")
	port := flag.Int("port", 3000, "HTTP port (when --transport=http)")
	yangDir := flag.String("yang-dir", "", "directory containing FRR .yang files (optional, enables frr://yang/* resources)")
	clientName := flag.String("client-name", "routing-mcp", "mgmtd client name (visible in `show mgmt clients`)")
	dialTimeout := flag.Duration("dial-timeout", 10*time.Second, "maximum time to wait for the mgmtd socket")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn := frrmgmt.Dial(ctx, *sockPath)
	defer conn.Close()

	// Wait for the initial connection.
	deadline := time.Now().Add(*dialTimeout)
	for !conn.IsConnected() && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			fmt.Fprintln(os.Stderr, "routing-mcp: interrupted before connecting")
			os.Exit(1)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if !conn.IsConnected() {
		fmt.Fprintf(os.Stderr, "routing-mcp: could not connect to %s within %s\n",
			*sockPath, *dialTimeout)
		os.Exit(1)
	}

	d := frrmgmt.NewDispatcher(conn.Frames())

	sessCtx, sessCancel := context.WithTimeout(ctx, 5*time.Second)
	sess, err := frrmgmt.New(sessCtx, conn, d, *clientName)
	sessCancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "routing-mcp: session: %v\n", err)
		os.Exit(1)
	}
	defer sess.Close(context.Background()) //nolint:errcheck

	client := frrmgmt.NewClient(sess, d, conn)

	srv, err := rmcp.NewServer(ctx, client, *yangDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "routing-mcp: server: %v\n", err)
		os.Exit(1)
	}

	switch *transport {
	case "stdio":
		if err := srv.ServeStdio(); err != nil {
			log.Fatal(err)
		}
	case "http":
		addr := fmt.Sprintf(":%d", *port)
		log.Printf("routing-mcp: HTTP/SSE on %s", addr)
		if err := srv.ServeHTTP(addr); err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "routing-mcp: unknown transport %q (want stdio or http)\n", *transport)
		os.Exit(1)
	}
}

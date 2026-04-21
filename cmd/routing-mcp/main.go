package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	sockPath := flag.String("socket", "/run/frr/mgmtd_fe.sock", "path to mgmtd_fe.sock")
	transport := flag.String("transport", "stdio", "MCP transport: stdio or http")
	port := flag.Int("port", 3000, "HTTP port (when --transport=http)")
	flag.Parse()

	fmt.Fprintf(os.Stderr, "routing-mcp: socket=%s transport=%s port=%d\n", *sockPath, *transport, *port)
	fmt.Fprintln(os.Stderr, "routing-mcp: not yet implemented — see PLAN.md")
	os.Exit(1)
}

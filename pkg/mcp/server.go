// Package mcp wraps frrmgmt.Client operations as MCP tools and resources.
//
// Tools:
//
//	get_config       - GetData(xpath, Running, FlagConfig)
//	get_state        - GetData(xpath, Operational, FlagState)
//	set_config       - EditAndCommit(xpath, Merge, data)
//	delete_config    - EditAndCommit(xpath, Delete, nil)
//	validate_config  - Lock → Edit → Commit(Validate) → Commit(Abort, unlock)
//	get_notifications - drain Subscribe channel (polling)
//	run_rpc          - RPC(xpath, input)
//
// Resources:
//
//	frr://yang/index        - list of available YANG modules
//	frr://yang/{module}     - contents of a specific .yang file
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cmoberg/routing-mcp/pkg/frrmgmt"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	version     = "0.1.0"
	maxNotifBuf = 1000
)

// Server is the routing-mcp MCP server. It wraps a frrmgmt.Client and
// exposes its operations as MCP tools. Notifications from mgmtd are buffered
// internally and drained by the get_notifications tool.
type Server struct {
	client  *frrmgmt.Client
	yangDir string
	mcp     *server.MCPServer

	notifMu  sync.Mutex
	notifBuf []frrmgmt.Notification
}

// NewServer creates and configures the MCP server. It subscribes to mgmtd
// notifications immediately using ctx (expected to live for the server
// lifetime). The YANG resource handlers are only registered when yangDir is a
// non-empty, accessible directory.
func NewServer(ctx context.Context, client *frrmgmt.Client, yangDir string) (*Server, error) {
	s := &Server{
		client:  client,
		yangDir: yangDir,
	}

	// Subscribe to all notifications. The channel is drained by a background
	// goroutine and the results buffered for get_notifications.
	notifCh, err := client.Subscribe(ctx, []string{"/"}, true)
	if err != nil {
		// Non-fatal: some FRR versions may reject "/" as a selector.
		// The get_notifications tool will return empty results.
		notifCh = make(chan frrmgmt.Notification)
	}
	go s.collectNotifications(notifCh)

	s.mcp = s.buildMCPServer()
	return s, nil
}

// ServeStdio runs the server on stdin/stdout (primary MCP transport).
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcp)
}

// ServeHTTP runs the server as an SSE HTTP server on addr (e.g. ":3000").
func (s *Server) ServeHTTP(addr string) error {
	sse := server.NewSSEServer(s.mcp, server.WithBaseURL("http://localhost"+addr))
	return sse.Start(addr)
}

func (s *Server) collectNotifications(ch <-chan frrmgmt.Notification) {
	for n := range ch {
		s.notifMu.Lock()
		if len(s.notifBuf) >= maxNotifBuf {
			s.notifBuf = s.notifBuf[1:] // drop oldest
		}
		s.notifBuf = append(s.notifBuf, n)
		s.notifMu.Unlock()
	}
}

func (s *Server) buildMCPServer() *server.MCPServer {
	srv := server.NewMCPServer("routing-mcp", version,
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(true, false),
	)

	srv.AddTool(mcp.NewTool("get_config",
		mcp.WithDescription("Read FRR configuration from the running datastore for a YANG xpath. "+
			"Returns JSON-encoded YANG tree. Use YANG module-prefixed paths, e.g. /frr-staticd:lib."),
		mcp.WithString("xpath",
			mcp.Required(),
			mcp.Description("YANG xpath, e.g. /frr-staticd:lib/route-list[prefix='10.0.0.0/8']"),
		),
	), s.handleGetConfig)

	srv.AddTool(mcp.NewTool("get_state",
		mcp.WithDescription("Read FRR operational state for a YANG xpath. "+
			"Returns JSON-encoded YANG tree. Use YANG module-prefixed paths, e.g. /frr-zebra:zebra."),
		mcp.WithString("xpath",
			mcp.Required(),
			mcp.Description("YANG xpath, e.g. /frr-zebra:zebra/interface[name='eth0']"),
		),
	), s.handleGetState)

	srv.AddTool(mcp.NewTool("set_config",
		mcp.WithDescription("Apply a configuration change by merging JSON data at an xpath into the "+
			"running configuration. Combines lock → edit → commit atomically."),
		mcp.WithString("xpath",
			mcp.Required(),
			mcp.Description("YANG xpath identifying the node to create or update"),
		),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description("JSON-encoded YANG subtree for the node"),
		),
	), s.handleSetConfig)

	srv.AddTool(mcp.NewTool("delete_config",
		mcp.WithDescription("Delete configuration at an xpath. "+
			"Combines lock → edit(delete) → commit atomically."),
		mcp.WithString("xpath",
			mcp.Required(),
			mcp.Description("YANG xpath identifying the node to delete"),
		),
	), s.handleDeleteConfig)

	srv.AddTool(mcp.NewTool("validate_config",
		mcp.WithDescription("Check whether a proposed configuration change is valid without applying it. "+
			"Performs lock → edit → commit(validate) → commit(abort) and reports pass or failure reason."),
		mcp.WithString("xpath",
			mcp.Required(),
			mcp.Description("YANG xpath identifying the node to validate"),
		),
		mcp.WithString("data",
			mcp.Required(),
			mcp.Description("JSON-encoded YANG subtree to validate"),
		),
	), s.handleValidateConfig)

	srv.AddTool(mcp.NewTool("get_notifications",
		mcp.WithDescription("Drain buffered mgmtd configuration-change notifications. "+
			"Returns a JSON array of notification objects (op, xpath, data). "+
			"Call repeatedly to poll; the buffer holds up to 1000 entries."),
		mcp.WithNumber("max_count",
			mcp.Description("Maximum number of notifications to return (default 10)"),
			mcp.DefaultNumber(10),
		),
	), s.handleGetNotifications)

	srv.AddTool(mcp.NewTool("run_rpc",
		mcp.WithDescription("Execute a YANG RPC or action at xpath. "+
			"Returns JSON-encoded output, or empty if the RPC has no output."),
		mcp.WithString("xpath",
			mcp.Required(),
			mcp.Description("YANG RPC or action xpath, e.g. /frr-zebra:zebra/clear-route"),
		),
		mcp.WithString("input",
			mcp.Description("JSON-encoded RPC input parameters (omit or pass null if none)"),
		),
	), s.handleRPC)

	// YANG file resources — only when yangDir is available.
	if s.yangDir != "" {
		if info, err := os.Stat(s.yangDir); err == nil && info.IsDir() {
			srv.AddResource(
				mcp.NewResource("frr://yang/index", "FRR YANG Module Index",
					mcp.WithResourceDescription("Lists all FRR YANG modules available in the local yang directory"),
					mcp.WithMIMEType("application/json"),
				),
				s.handleYangIndex,
			)
			srv.AddResourceTemplate(
				mcp.NewResourceTemplate("frr://yang/{module}", "FRR YANG Module",
					mcp.WithTemplateDescription("Contents of an FRR YANG file. Use the module name from frr://yang/index."),
					mcp.WithTemplateMIMEType("text/plain"),
				),
				s.handleYangModule,
			)
		}
	}

	return srv
}

// ─── Tool handlers ────────────────────────────────────────────────────────────

func (s *Server) handleGetConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xpath, err := req.RequireString("xpath")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := s.client.GetData(ctx, xpath, frrmgmt.DatastoreRunning, frrmgmt.GetDataFlagConfig)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(data) == 0 {
		return mcp.NewToolResultText("{}"), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleGetState(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xpath, err := req.RequireString("xpath")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := s.client.GetData(ctx, xpath, frrmgmt.DatastoreOperational, frrmgmt.GetDataFlagState)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(data) == 0 {
		return mcp.NewToolResultText("{}"), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

func (s *Server) handleSetConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xpath, err := req.RequireString("xpath")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := req.RequireString("data")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := s.client.EditAndCommit(ctx, xpath, frrmgmt.EditOpMerge, []byte(data))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("ok: changed=%v created=%v xpath=%q",
		result.Changed, result.Created, result.XPath)), nil
}

func (s *Server) handleDeleteConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xpath, err := req.RequireString("xpath")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := s.client.EditAndCommit(ctx, xpath, frrmgmt.EditOpDelete, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcp.NewToolResultText(fmt.Sprintf("ok: changed=%v xpath=%q",
		result.Changed, result.XPath)), nil
}

func (s *Server) handleValidateConfig(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xpath, err := req.RequireString("xpath")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	data, err := req.RequireString("data")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if err := s.client.Lock(ctx, frrmgmt.DatastoreCandidate); err != nil {
		return mcp.NewToolResultError("lock: " + err.Error()), nil
	}
	// Always abort + unlock regardless of what happens next.
	defer s.client.Commit(context.Background(), frrmgmt.CommitAbort, true) //nolint:errcheck

	if _, err := s.client.Edit(ctx, xpath, frrmgmt.EditOpMerge, []byte(data)); err != nil {
		return mcp.NewToolResultError("edit: " + err.Error()), nil
	}
	if err := s.client.Commit(ctx, frrmgmt.CommitValidate, false); err != nil {
		return mcp.NewToolResultError("invalid: " + err.Error()), nil
	}
	return mcp.NewToolResultText("valid"), nil
}

type notifJSON struct {
	Op    uint8           `json:"op"`
	XPath string          `json:"xpath"`
	Data  json.RawMessage `json:"data,omitempty"`
}

func (s *Server) handleGetNotifications(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	maxCount := req.GetInt("max_count", 10)
	if maxCount <= 0 {
		maxCount = 10
	}

	s.notifMu.Lock()
	n := min(maxCount, len(s.notifBuf))
	batch := make([]frrmgmt.Notification, n)
	copy(batch, s.notifBuf[:n])
	s.notifBuf = s.notifBuf[n:]
	s.notifMu.Unlock()

	out := make([]notifJSON, len(batch))
	for i, notif := range batch {
		nj := notifJSON{Op: notif.Op, XPath: notif.XPath}
		if len(notif.Data) > 0 {
			nj.Data = json.RawMessage(notif.Data)
		}
		out[i] = nj
	}
	b, err := json.Marshal(out)
	if err != nil {
		return mcp.NewToolResultError("marshal: " + err.Error()), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

func (s *Server) handleRPC(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	xpath, err := req.RequireString("xpath")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var input []byte
	if raw := req.GetString("input", ""); raw != "" && raw != "null" {
		input = []byte(raw)
	}
	out, err := s.client.RPC(ctx, xpath, input)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if len(out) == 0 {
		return mcp.NewToolResultText("{}"), nil
	}
	return mcp.NewToolResultText(string(out)), nil
}

// ─── Resource handlers ────────────────────────────────────────────────────────

func (s *Server) handleYangIndex(_ context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	entries, err := os.ReadDir(s.yangDir)
	if err != nil {
		return nil, fmt.Errorf("mcp: reading yang dir: %w", err)
	}
	var modules []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yang") {
			modules = append(modules, strings.TrimSuffix(e.Name(), ".yang"))
		}
	}
	b, _ := json.Marshal(modules)
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(b),
		},
	}, nil
}

func (s *Server) handleYangModule(_ context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	// URI: frr://yang/{module}
	module := strings.TrimPrefix(req.Params.URI, "frr://yang/")
	if module == "" || strings.ContainsAny(module, "/\\") {
		return nil, fmt.Errorf("mcp: invalid module name")
	}
	path := filepath.Join(s.yangDir, module+".yang")
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mcp: reading %s: %w", path, err)
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/plain",
			Text:     string(content),
		},
	}, nil
}

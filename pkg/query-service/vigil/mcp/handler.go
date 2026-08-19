package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SigNoz/signoz/pkg/query-service/vigil"
)

// ToolHandler is a function that executes an MCP tool with cost tracking.
type ToolHandler func(ctx context.Context, args map[string]any) (*CallToolResult, error)

// MCPServer is the core MCP protocol handler that manages tools, resources,
// and client connections. It tracks costs and emits events to the Vigil
// control plane via an EventCallback.
type MCPServer struct {
	logger    *slog.Logger
	tools     []Tool
	handlers  map[string]ToolHandler
	resources []Resource

	// mu guards clients and every field of the sessions it holds. Sessions are
	// only ever handed out as copies (see Sessions/Session) and only ever
	// mutated inside UpdateSession, so no caller can race on one.
	mu      sync.RWMutex
	clients map[string]*ClientSession

	costCallback  func(agentID string, toolCost, sessionTotal float64, tool string)
	eventCallback func(eventType string, data any)
	projectRoot   string
	defaultBudget float64

	firewall  FirewallFn
	commit    CommitFn
	behaviour BehaviourFn
}

// ClientSession represents a connected MCP client (e.g., Claude Desktop).
//
// It holds no lock of its own so that it stays safe to copy; synchronization is
// the owning MCPServer's job.
type ClientSession struct {
	ID            string
	ClientName    string
	ClientVersion string
	ConnectedAt   time.Time
	TotalCost     float64
	ToolCallCount int
	BudgetLimit   float64
	Blocked       bool

	// Demo marks sessions created by the local demo harness so the dashboard
	// can label their events. Set from the client's own declared name during
	// initialize, not from a global mode flag — a real agent connecting while
	// the demo runs must not be mislabeled.
	Demo bool
}

// DefaultSessionBudget is the per-session spend ceiling applied to a client
// that arrives without one (i.e. outside the OAuth consent flow, which carries
// its own approved budget). Overridable with VIGIL_BUDGET_LIMIT.
const DefaultSessionBudget = 5.0

// NewMCPServer creates a new MCP server wired with default tools.
func NewMCPServer(logger *slog.Logger, projectRoot string) *MCPServer {
	s := &MCPServer{
		logger:        logger,
		tools:         DefaultTools(),
		handlers:      make(map[string]ToolHandler),
		clients:       make(map[string]*ClientSession),
		projectRoot:   projectRoot,
		defaultBudget: DefaultSessionBudget,
	}
	if v := vigil.Env("BUDGET_LIMIT"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil && parsed > 0 {
			s.defaultBudget = parsed
		}
	}

	// Register default tool handlers
	s.registerHandlers()
	return s
}

// SetCostCallback sets a function called when a tool call incurs cost.
// The Vigil server uses this to track spend through the cost firewall.
//
// sessionTotal is this session's own running total. Callers must use it rather
// than their own accumulator when attributing spend to an agent, otherwise
// every agent is reported as having spent the fleet-wide total.
func (s *MCPServer) SetCostCallback(fn func(agentID string, toolCost, sessionTotal float64, tool string)) {
	s.costCallback = fn
}

// SetEventCallback sets a function called when MCP events occur (connect, disconnect, tool call, block).
// The Vigil server uses this to stream events to the Next.js frontend via WebSocket.
func (s *MCPServer) SetEventCallback(fn func(eventType string, data any)) {
	s.eventCallback = fn
}

// FirewallInput is the context handed to the firewall for one tool call.
type FirewallInput struct {
	SessionID   string
	Tool        string
	Args        map[string]any
	ToolCost    float64
	SessionCost float64
	Budget      float64
}

// FirewallVerdict is the firewall's answer, reduced to what MCP can express.
//
// MCP's tools/call has exactly two outcomes — a result or an error result — so
// PAUSE and BLOCK both surface as an error result and differ only in whether
// the session is latched closed (BlockSession).
type FirewallVerdict struct {
	Allow        bool
	BlockSession bool
	Decision     string // ALLOW | PAUSE | BLOCK | FALLBACK
	Reason       string
	Message      string // agent-facing text when not allowed
}

// FirewallFn decides whether a tool call may proceed.
type FirewallFn func(ctx context.Context, in FirewallInput) FirewallVerdict

// CommitFn records the outcome of a tool call that actually executed, so the
// firewall can update the session's behavioral and cost history.
type CommitFn func(sessionID, tool string, cost float64, dur time.Duration, ok bool)

// BehaviourFn returns whatever the firewall has observed for a session. Any
// JSON-encodable value; declared as `any` so mcp does not import firewall.
type BehaviourFn func(sessionID string) any

// SetBehaviour installs the source for the vigil_agent_dna tool.
func (s *MCPServer) SetBehaviour(fn BehaviourFn) { s.behaviour = fn }

// SetFirewall installs the governance hook. Both funcs are optional; with no
// firewall installed the server behaves as it did before 2.0.
//
// Deliberately plain funcs rather than an interface from the firewall package:
// appserver imports both mcp and firewall, so a direct dependency here would
// create an import cycle.
func (s *MCPServer) SetFirewall(check FirewallFn, commit CommitFn) {
	s.firewall = check
	s.commit = commit
}

// Sessions returns a copy of every connected client session.
func (s *MCPServer) Sessions() []ClientSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ClientSession, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, *c)
	}
	return out
}

// Session returns a copy of one session.
func (s *MCPServer) Session(id string) (ClientSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.clients[id]
	if !ok {
		return ClientSession{}, false
	}
	return *c, true
}

// PutSession inserts or replaces a session. Used by the OAuth flow to
// pre-register a session before the client's first JSON-RPC call, and by the
// SSE transport on connect.
func (s *MCPServer) PutSession(sess ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := sess
	s.clients[sess.ID] = &cp
}

// DropSession removes a session. Must be called on disconnect; without it the
// session table grows without bound.
func (s *MCPServer) DropSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.clients, id)
}

// UpdateSession applies fn to a session under the lock, reporting whether the
// session existed. This is the only sanctioned way to mutate session state.
func (s *MCPServer) UpdateSession(id string, fn func(*ClientSession)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.clients[id]
	if !ok {
		return false
	}
	fn(c)
	return true
}

// ensureSession returns the session for id, creating it with defaultBudget if
// absent. The find-or-create runs under a single write lock; splitting it into
// a read followed by a write is the check-then-act race this replaces.
func (s *MCPServer) ensureSession(id string, defaultBudget float64) ClientSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[id]; ok {
		return *c
	}
	c := &ClientSession{ID: id, ConnectedAt: time.Now(), BudgetLimit: defaultBudget}
	s.clients[id] = c
	return *c
}

// registerHandlers wires the default tool implementations.
func (s *MCPServer) registerHandlers() {
	s.handlers["read_file"] = s.handleReadFile
	s.handlers["search_code"] = s.handleSearchCode
	s.handlers["list_directory"] = s.handleListDirectory
	s.handlers["analyze_codebase"] = s.handleAnalyzeCodebase
	s.handlers["run_command"] = s.handleRunCommand
	s.handlers["signoz_query_traces"] = s.handleSigNozQueryTraces
	s.handlers["signoz_get_services"] = s.handleSigNozGetServices
	s.handlers["signoz_list_alerts"] = s.handleSigNozListAlerts
	s.handlers["signoz_create_dashboard"] = s.handleSigNozCreateDashboard
	s.handlers["vigil_list_agents"] = s.handleArgusListAgents
	s.handlers["vigil_agent_dna"] = s.handleArgusAgentDNA
	s.handlers["vigil_cost_status"] = s.handleArgusCostStatus
}

// HandleRequest processes a JSON-RPC request and returns a response.
// This is the main entry point for the MCP protocol.
func (s *MCPServer) HandleRequest(ctx context.Context, raw json.RawMessage, clientID string) *Response {
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return NewErrorResponse(nil, ErrCodeParse, "Parse error: invalid JSON-RPC")
	}

	if req.JSONRPC != JSONRPCVersion {
		return NewErrorResponse(req.ID, ErrCodeInvalidRequest, "Invalid JSON-RPC version")
	}

	session := s.ensureSession(clientID, s.defaultBudget)

	switch req.Method {
	case MethodInitialize:
		return s.handleInitialize(req.ID, req.Params, session.ID)
	case MethodInitialized:
		return s.handleInitialized(req.ID, session.ID)
	case MethodToolsList:
		return s.handleToolsList(req.ID)
	case MethodToolsCall:
		return s.handleToolsCall(ctx, req.ID, req.Params, session.ID)
	case MethodResourcesList:
		return s.handleResourcesList(req.ID)
	case MethodResourcesRead:
		return s.handleResourcesRead(req.ID, req.Params)
	case MethodPing:
		return NewResponse(req.ID, map[string]any{})
	default:
		return NewErrorResponse(req.ID, ErrCodeMethodNotFound, "Method not found: "+req.Method)
	}
}

// ---------- Method Handlers ----------

// DemoClientPrefix marks a client as the local demo harness. The harness sets
// its own clientInfo.name to something starting with this, so demo-generated
// events are labeled at the source rather than by a server-wide mode flag.
const DemoClientPrefix = "Vigil Demo"

func (s *MCPServer) handleInitialize(id json.RawMessage, params json.RawMessage, sessionID string) *Response {
	var req InitializeRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return NewErrorResponse(id, ErrCodeInvalidParams, "Invalid initialize params")
	}

	s.UpdateSession(sessionID, func(c *ClientSession) {
		c.ClientName = req.ClientInfo.Name
		c.ClientVersion = req.ClientInfo.Version
		c.Demo = strings.HasPrefix(req.ClientInfo.Name, DemoClientPrefix)
	})
	session, _ := s.Session(sessionID)

	result := InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities: ServerCapabilities{
			Tools:     &ToolCapabilities{ListChanged: true},
			Resources: &ResourceCapabilities{Subscribe: false, ListChanged: false},
		},
		ServerInfo: Implementation{
			Name:    "Vigil Runtime Firewall",
			Version: "2.0.0",
		},
		Instructions: `Vigil is a runtime firewall that sits between agents and their tools. Every tool call is checked against the session's declared intent, its behavioral baseline, and a live cost budget before it executes, then allowed, paused, or blocked. Decisions are recorded in a tamper-evident audit chain.`,
	}

	s.emitEvent("mcp_client_connected", map[string]any{
		"client_id":   session.ID,
		"client_name": session.ClientName,
		"version":     session.ClientVersion,
		"time":        session.ConnectedAt,
		"demo":        session.Demo,
	})

	return NewResponse(id, result)
}

func (s *MCPServer) handleInitialized(id json.RawMessage, sessionID string) *Response {
	session, _ := s.Session(sessionID)
	s.logger.InfoContext(context.Background(), "mcp: client initialized",
		slog.String("client_id", session.ID),
		slog.String("client_name", session.ClientName),
	)
	return nil // notifications don't get a response
}

func (s *MCPServer) handleToolsList(id json.RawMessage) *Response {
	return NewResponse(id, ListToolsResult{Tools: s.tools})
}

// handleToolsCall runs the governed tool-call path.
//
// Ordering matters and differs from the pre-2.0 behavior: the firewall decides
// before anything is charged or executed, and cost is applied only after the
// handler has actually run. Previously a call could be charged and then refused
// without executing, so a session's recorded spend included work never done.
func (s *MCPServer) handleToolsCall(ctx context.Context, id json.RawMessage, params json.RawMessage, sessionID string) *Response {
	session, ok := s.Session(sessionID)
	if !ok {
		return NewErrorResponse(id, ErrCodeInternal, "Unknown session")
	}

	// A session blocked by an earlier decision stays blocked until a human
	// releases it via the session approval endpoint.
	if session.Blocked {
		return NewResponse(id, NewErrorResult(fmt.Sprintf(
			"Vigil: this session is blocked (spent $%.4f of a $%.2f budget). An operator must approve it to continue.",
			session.TotalCost, session.BudgetLimit,
		)))
	}

	var req CallToolRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return NewErrorResponse(id, ErrCodeInvalidParams, "Invalid tool call params")
	}

	handler, exists := s.handlers[req.Name]
	if !exists {
		return NewErrorResponse(id, ErrCodeMethodNotFound, "Tool not found: "+req.Name)
	}

	toolCost := ToolCost(req.Name)

	// Firewall decision, before any charge and before execution.
	if s.firewall != nil {
		v := s.firewall(ctx, FirewallInput{
			SessionID:   session.ID,
			Tool:        req.Name,
			Args:        req.Arguments,
			ToolCost:    toolCost,
			SessionCost: session.TotalCost,
			Budget:      session.BudgetLimit,
		})
		if !v.Allow {
			if v.BlockSession {
				s.UpdateSession(session.ID, func(c *ClientSession) { c.Blocked = true })
			}
			s.emitEvent("mcp_call_blocked", map[string]any{
				"client_id": session.ID,
				"tool":      req.Name,
				"decision":  v.Decision,
				"reason":    v.Reason,
				"demo":      session.Demo,
			})
			return NewResponse(id, NewErrorResult(v.Message))
		}
	}

	start := time.Now()
	result, err := handler(ctx, req.Arguments)
	elapsed := time.Since(start)

	// Charge only for work that actually ran.
	var total float64
	var count int
	var tripped bool
	s.UpdateSession(session.ID, func(c *ClientSession) {
		c.TotalCost += toolCost
		c.ToolCallCount++
		total, count = c.TotalCost, c.ToolCallCount
		if c.TotalCost > c.BudgetLimit && !c.Blocked {
			c.Blocked = true
			tripped = true
		}
	})

	if s.costCallback != nil {
		s.costCallback(session.ID, toolCost, total, req.Name)
	}
	if s.commit != nil {
		s.commit(session.ID, req.Name, toolCost, elapsed, err == nil)
	}

	s.emitEvent("mcp_tool_call", map[string]any{
		"client_id":  session.ID,
		"tool":       req.Name,
		"cost":       toolCost,
		"total":      total,
		"budget":     session.BudgetLimit,
		"count":      count,
		"latency_ms": elapsed.Milliseconds(),
		"demo":       session.Demo,
	})

	if tripped {
		s.emitEvent("mcp_budget_exceeded", map[string]any{
			"client_id": session.ID,
			"total":     total,
			"budget":    session.BudgetLimit,
			"demo":      session.Demo,
		})
	}

	if err != nil {
		return NewResponse(id, NewErrorResult("Tool error: "+err.Error()))
	}
	return NewResponse(id, result)
}

func (s *MCPServer) handleResourcesList(id json.RawMessage) *Response {
	return NewResponse(id, ListResourcesResult{Resources: s.resources})
}

func (s *MCPServer) handleResourcesRead(id json.RawMessage, params json.RawMessage) *Response {
	var req ReadResourceRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return NewErrorResponse(id, ErrCodeInvalidParams, "Invalid resource read params")
	}

	for _, r := range s.resources {
		if r.URI == req.URI {
			return NewResponse(id, ReadResourceResult{
				Contents: []ResourceContent{{URI: r.URI, MimeType: r.MimeType, Text: "Resource content"}},
			})
		}
	}
	return NewErrorResponse(id, ErrCodeInvalidParams, "Resource not found: "+req.URI)
}

// ---------- Tool Handlers ----------

func (s *MCPServer) handleReadFile(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	path, _ := args["path"].(string)
	fullPath, err := s.resolveInRoot(path)
	if err != nil {
		return NewErrorResult(err.Error()), nil
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("Failed to read file: %s", err.Error())), nil
	}

	// Truncate very large files
	content := string(data)
	if len(content) > 50000 {
		content = content[:50000] + "\n\n... [truncated at 50,000 characters]"
	}

	return NewTextResult(content), nil
}

func (s *MCPServer) handleSearchCode(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return NewErrorResult("pattern is required"), nil
	}

	glob, _ := args["glob"].(string)
	caseSensitive, _ := args["case_sensitive"].(bool)

	cmdArgs := []string{"--line-number", "--color=never"}
	if !caseSensitive {
		cmdArgs = append(cmdArgs, "-i")
	}
	if glob != "" {
		cmdArgs = append(cmdArgs, "-g", glob)
	}
	cmdArgs = append(cmdArgs, pattern)

	if s.projectRoot != "" {
		cmdArgs = append(cmdArgs, s.projectRoot)
	}

	cmd := exec.CommandContext(ctx, "rg", cmdArgs...)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			// rg returns exit code 1 when no matches found
			return NewTextResult("No matches found"), nil
		}
		return NewErrorResult(fmt.Sprintf("Search failed: %s", err.Error())), nil
	}

	result := string(output)
	if len(result) > 10000 {
		result = result[:10000] + "\n\n... [truncated at 10,000 characters]"
	}

	if len(result) == 0 {
		return NewTextResult("No matches found"), nil
	}

	return NewTextResult(result), nil
}

func (s *MCPServer) handleListDirectory(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	path, _ := args["path"].(string)
	fullPath, err := s.resolveInRoot(path)
	if err != nil {
		return NewErrorResult(err.Error()), nil
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("Failed to list directory: %s", err.Error())), nil
	}

	var result strings.Builder
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		prefix := "📄"
		if entry.IsDir() {
			prefix = "📁"
		}
		result.WriteString(fmt.Sprintf("%s %s  (%d bytes, mod %s)\n", prefix, entry.Name(), info.Size(), info.ModTime().Format("Jan 02 15:04")))
	}

	return NewTextResult(result.String()), nil
}

func (s *MCPServer) handleAnalyzeCodebase(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	rootDir := s.projectRoot
	if r, ok := args["root_dir"].(string); ok && r != "" {
		rootDir = r
	}
	if rootDir == "" {
		return NewErrorResult("No root directory configured"), nil
	}

	depth := 3
	if d, ok := args["depth"].(float64); ok && d > 0 {
		depth = int(d)
		if depth > 6 {
			depth = 6
		}
	}

	// Count files by extension with depth-limited walking
	extCount := make(map[string]int)
	filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		// Calculate current depth relative to rootDir
		relPath := strings.TrimPrefix(path, rootDir)
		relPath = strings.TrimPrefix(relPath, "/")
		currentDepth := strings.Count(relPath, string(filepath.Separator))

		if info.IsDir() {
			// Skip hidden directories and enforce depth limit
			if strings.HasPrefix(info.Name(), ".") && path != rootDir {
				return filepath.SkipDir
			}
			if currentDepth >= depth {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext != "" {
			extCount[ext]++
		}
		return nil
	})

	var result strings.Builder
	result.WriteString(fmt.Sprintf("📊 Codebase Analysis: %s\n\n", rootDir))
	result.WriteString(fmt.Sprintf("Max Depth: %d\n\n", depth))
	result.WriteString("## File Count by Extension\n\n")
	for ext, count := range extCount {
		result.WriteString(fmt.Sprintf("- %s: %d files\n", ext, count))
	}
	result.WriteString(fmt.Sprintf("\nTotal unique extensions: %d\n", len(extCount)))

	return NewTextResult(result.String()), nil
}

func (s *MCPServer) handleRunCommand(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	command, _ := args["command"].(string)
	if command == "" {
		return NewErrorResult("command is required"), nil
	}
	if refusal := checkCommand(command); refusal != "" {
		return NewErrorResult(refusal), nil
	}

	timeout := 30
	if t, ok := args["timeout_seconds"].(float64); ok && t > 0 {
		timeout = int(t)
		if timeout > 120 {
			timeout = 120
		}
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	if s.projectRoot != "" {
		cmd.Dir = s.projectRoot
	}

	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return NewErrorResult(fmt.Sprintf("Command timed out after %d seconds", timeout)), nil
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return NewTextResult(fmt.Sprintf("Command exited with code %d\n%s", exitErr.ExitCode(), string(exitErr.Stderr))), nil
		}
		return NewErrorResult(fmt.Sprintf("Command failed: %s", err.Error())), nil
	}

	result := string(output)
	if len(result) > 20000 {
		result = result[:20000] + "\n\n... [truncated at 20,000 characters]"
	}
	return NewTextResult(result), nil
}

// The SigNoz-backed tools require a ClickHouse telemetry store. The standalone
// binary does not wire one, so rather than returning formatted markdown with no
// data in it -- which an agent cannot distinguish from a real answer -- they
// report unavailability explicitly. A governance product that lets its own
// tools fake success has lost the argument.
func (s *MCPServer) signozUnavailable(tool string) *CallToolResult {
	return NewErrorResult(fmt.Sprintf(
		"%s is unavailable: no ClickHouse telemetry store is configured on this deployment. "+
			"Set VIGIL_CLICKHOUSE_DSN and run inside the SigNoz query service to enable it.", tool))
}

func (s *MCPServer) handleSigNozQueryTraces(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	return s.signozUnavailable("signoz_query_traces"), nil
}

func (s *MCPServer) handleSigNozGetServices(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	return s.signozUnavailable("signoz_get_services"), nil
}

func (s *MCPServer) handleSigNozListAlerts(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	return s.signozUnavailable("signoz_list_alerts"), nil
}

func (s *MCPServer) handleSigNozCreateDashboard(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	return s.signozUnavailable("signoz_create_dashboard"), nil
}

func (s *MCPServer) handleArgusListAgents(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	sessions := s.Sessions()
	var agents strings.Builder
	agents.WriteString("## Vigil Connected Agents\n\n")
	agents.WriteString("| Client ID | Client Name | Connected | Cost | Calls | Blocked |\n")
	agents.WriteString("|-----------|-------------|-----------|------|-------|--------|\n")
	for _, c := range sessions {
		blocked := "No"
		if c.Blocked {
			blocked = "Yes"
		}
		agents.WriteString(fmt.Sprintf("| %s | %s | %s | $%.4f | %d | %s |\n",
			c.ID[:min(len(c.ID), 8)], c.ClientName, c.ConnectedAt.Format("15:04:05"), c.TotalCost, c.ToolCallCount, blocked))
	}
	if len(sessions) == 0 {
		agents.WriteString("_No agents currently connected._\n")
	}
	return NewTextResult(agents.String()), nil
}

func (s *MCPServer) handleArgusAgentDNA(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		sessionID, _ = args["trace_id"].(string)
	}
	if sessionID == "" {
		return NewErrorResult("session_id is required"), nil
	}
	if s.behaviour == nil {
		return NewErrorResult("behavioural profiling is not wired on this deployment"), nil
	}

	b := s.behaviour(sessionID)
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return NewErrorResult("could not encode profile: " + err.Error()), nil
	}
	// `observed:false` is reported rather than hidden: a caller must be able to
	// tell "this session did nothing" from "we have no record of it".
	return NewTextResult(fmt.Sprintf("## Agent DNA — observed behaviour\n\n```json\n%s\n```\n", raw)), nil
}

func (s *MCPServer) handleArgusCostStatus(ctx context.Context, args map[string]any) (*CallToolResult, error) {
	sessions := s.Sessions()
	var totalCost float64
	var totalCalls, blockedCount int
	for _, c := range sessions {
		totalCost += c.TotalCost
		totalCalls += c.ToolCallCount
		if c.Blocked {
			blockedCount++
		}
	}

	result := fmt.Sprintf(`## Vigil Cost Firewall Status

- Total Cost: $%.4f
- Total Tool Calls: %d
- Connected Agents: %d
- Blocked Agents: %d
- Default Budget per Session: $%.2f
`, totalCost, totalCalls, len(sessions), blockedCount, s.defaultBudget)

	return NewTextResult(result), nil
}

// ---------- Helpers ----------

func (s *MCPServer) emitEvent(eventType string, data any) {
	if s.eventCallback != nil {
		s.eventCallback(eventType, data)
	}
}

// Package mcp implements a Model Context Protocol server for Beecon,
// enabling AI agents to interact with Beecon's infrastructure engine
// via the standard MCP tool-use protocol over stdio.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/logging"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// Server wraps an engine.Engine and exposes its operations as MCP tools.
type Server struct {
	eng *engine.Engine
	mcp *server.MCPServer
	mu  sync.Mutex // guards ActiveProfile mutation + engine call atomicity
}

// New creates a new MCP server backed by the given engine.
func New(eng *engine.Engine, version string) *Server {
	s := &Server{eng: eng}
	s.mcp = server.NewMCPServer(
		"beecon",
		version,
		server.WithToolCapabilities(true),
	)
	s.registerTools()
	return s
}

// Serve starts the MCP server on stdio (stdin/stdout). Blocks until context is
// cancelled or stdin is closed.
func (s *Server) Serve() error {
	return server.ServeStdio(s.mcp)
}

func (s *Server) registerTools() {
	// Read toolset: safe, no mutations.
	s.mcp.AddTool(toolValidateBeacon(), s.handleValidateBeacon)
	s.mcp.AddTool(toolPlan(), s.handlePlan)
	s.mcp.AddTool(toolShowStatus(), s.handleShowStatus)
	s.mcp.AddTool(toolDetectDrift(), s.handleDetectDrift)
	s.mcp.AddTool(toolListRuns(), s.handleListRuns)
	s.mcp.AddTool(toolListApprovals(), s.handleListApprovals)
	s.mcp.AddTool(toolGetHistory(), s.handleGetHistory)
	s.mcp.AddTool(toolDiscoverBeacons(), s.handleDiscoverBeacons)

	// Write toolset: mutating operations.
	s.mcp.AddTool(toolApply(), s.handleApply)
	s.mcp.AddTool(toolApprove(), s.handleApprove)
	s.mcp.AddTool(toolReject(), s.handleReject)
	s.mcp.AddTool(toolRollback(), s.handleRollback)

	// Manage toolset: setup/config.
	s.mcp.AddTool(toolConnectProvider(), s.handleConnectProvider)
}

// --- Tool Definitions ---

func toolValidateBeacon() mcp.Tool {
	return mcp.NewTool("validate_beacon",
		mcp.WithDescription("Validate a .beecon file for syntax and semantic correctness"),
		mcp.WithString("beacon_file", mcp.Required(), mcp.Description("Path to the .beecon file")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolPlan() mcp.Tool {
	return mcp.NewTool("plan",
		mcp.WithDescription("Generate an execution plan from a beacon file. Returns actions, cost estimates, risk scores, compliance impact, and rollback feasibility for each action."),
		mcp.WithString("beacon_file", mcp.Required(), mcp.Description("Path to the .beecon file")),
		mcp.WithString("profile", mcp.Description("Active profile name (e.g. production, staging)")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolShowStatus() mcp.Tool {
	return mcp.NewTool("show_status",
		mcp.WithDescription("Show current infrastructure state including all managed resources, their status, and drift information"),
		mcp.WithString("filter", mcp.Description("Filter by status: MATCHED, DRIFTED, UNPROVISIONED, OBSERVED")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolDetectDrift() mcp.Tool {
	return mcp.NewTool("detect_drift",
		mcp.WithDescription("Detect configuration drift between declared intent and current cloud state"),
		mcp.WithString("beacon_file", mcp.Required(), mcp.Description("Path to the .beecon file")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolListRuns() mcp.Tool {
	return mcp.NewTool("list_runs",
		mcp.WithDescription("List all execution runs with their status and action counts"),
		mcp.WithString("agent_id", mcp.Description("Filter runs by agent identity")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolListApprovals() mcp.Tool {
	return mcp.NewTool("list_approvals",
		mcp.WithDescription("List pending and resolved approval requests"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolGetHistory() mcp.Tool {
	return mcp.NewTool("get_history",
		mcp.WithDescription("Get the audit event history for a specific resource"),
		mcp.WithString("resource_id", mcp.Required(), mcp.Description("The resource ID to get history for")),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolDiscoverBeacons() mcp.Tool {
	return mcp.NewTool("discover_beacons",
		mcp.WithDescription("Discover all .beecon files in the current project"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolApply() mcp.Tool {
	return mcp.NewTool("apply",
		mcp.WithDescription("Execute the plan from a beacon file. Creates, updates, or deletes cloud resources. Actions requiring approval are deferred."),
		mcp.WithString("beacon_file", mcp.Required(), mcp.Description("Path to the .beecon file")),
		mcp.WithBoolean("force", mcp.Description("Bypass budget enforcement")),
		mcp.WithString("profile", mcp.Description("Active profile name")),
		mcp.WithString("agent_id", mcp.Description("Agent identity for audit trail (e.g. claude-opus-4-6)")),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(false),
	)
}

func toolApprove() mcp.Tool {
	return mcp.NewTool("approve",
		mcp.WithDescription("Approve a pending approval request, executing its deferred actions"),
		mcp.WithString("request_id", mcp.Required(), mcp.Description("The approval request ID")),
		mcp.WithString("approver", mcp.Description("Identity of the approver (default: mcp-agent)")),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func toolReject() mcp.Tool {
	return mcp.NewTool("reject",
		mcp.WithDescription("Reject a pending approval request"),
		mcp.WithString("request_id", mcp.Required(), mcp.Description("The approval request ID")),
		mcp.WithString("approver", mcp.Description("Identity of the rejector (default: mcp-agent)")),
		mcp.WithString("reason", mcp.Description("Reason for rejection")),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

func toolRollback() mcp.Tool {
	return mcp.NewTool("rollback",
		mcp.WithDescription("Rollback a previous run by executing inverse actions in reverse order"),
		mcp.WithString("run_id", mcp.Required(), mcp.Description("The run ID to rollback")),
		mcp.WithDestructiveHintAnnotation(true),
	)
}

func toolConnectProvider() mcp.Tool {
	return mcp.NewTool("connect_provider",
		mcp.WithDescription("Register and validate cloud provider credentials"),
		mcp.WithString("provider", mcp.Required(), mcp.Description("Cloud provider: aws, gcp, or azure")),
		mcp.WithString("region", mcp.Description("Cloud region (e.g. us-east-1)")),
		mcp.WithDestructiveHintAnnotation(false),
	)
}

// --- Tool Handlers ---

func (s *Server) handleValidateBeacon(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	beaconFile, _ := args["beacon_file"].(string)
	logging.Logger.Debug("mcp:tool", "name", "validate_beacon", "beacon_file", beaconFile)
	if beaconFile == "" {
		return mcp.NewToolResultError("beacon_file is required"), nil
	}
	if err := security.SafePath(s.eng.Root(), beaconFile); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "validate_beacon", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("invalid beacon_file: %s", err)), nil
	}

	if err := s.eng.Validate(beaconFile); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "validate_beacon", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("validation failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"valid": true,
		"path":  beaconFile,
	})
}

func (s *Server) handlePlan(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	beaconFile, _ := args["beacon_file"].(string)
	logging.Logger.Debug("mcp:tool", "name", "plan", "beacon_file", beaconFile)
	if beaconFile == "" {
		return mcp.NewToolResultError("beacon_file is required"), nil
	}
	if err := security.SafePath(s.eng.Root(), beaconFile); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "plan", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("invalid beacon_file: %s", err)), nil
	}

	s.mu.Lock()
	if profile, ok := args["profile"].(string); ok && profile != "" {
		s.eng.ActiveProfile = profile
	}
	res, err := s.eng.Plan(ctx, beaconFile)
	s.mu.Unlock()

	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "plan", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("plan failed: %s", err)), nil
	}

	return resultJSON(scrubPlanResult(res))
}

func (s *Server) handleShowStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logging.Logger.Debug("mcp:tool", "name", "show_status")
	st, err := s.eng.Status(ctx)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "show_status", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("status failed: %s", err)), nil
	}

	scrubState(st)

	args := req.GetArguments()
	if filter, ok := args["filter"].(string); ok && filter != "" {
		filtered := make(map[string]*state.ResourceRecord)
		for k, v := range st.Resources {
			if string(v.Status) == filter {
				filtered[k] = v
			}
		}
		st.Resources = filtered
	}

	return resultJSON(st)
}

func (s *Server) handleDetectDrift(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	beaconFile, _ := args["beacon_file"].(string)
	logging.Logger.Debug("mcp:tool", "name", "detect_drift", "beacon_file", beaconFile)
	if beaconFile == "" {
		return mcp.NewToolResultError("beacon_file is required"), nil
	}
	if err := security.SafePath(s.eng.Root(), beaconFile); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "detect_drift", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("invalid beacon_file: %s", err)), nil
	}

	drifted, errs, err := s.eng.Drift(ctx, beaconFile)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "detect_drift", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("drift detection failed: %s", err)), nil
	}

	for _, rec := range drifted {
		rec.IntentSnapshot = security.ScrubMap(rec.IntentSnapshot)
		rec.LiveState = security.ScrubMap(rec.LiveState)
	}
	errStrs := make([]string, len(errs))
	for i, e := range errs {
		errStrs[i] = e.Error()
	}

	return resultJSON(map[string]any{
		"drifted": drifted,
		"count":   len(drifted),
		"errors":  errStrs,
	})
}

func (s *Server) handleListRuns(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	logging.Logger.Debug("mcp:tool", "name", "list_runs")

	// If agent_id filter is provided, use AgentHistory.
	if agentID, ok := args["agent_id"].(string); ok && agentID != "" {
		runs, err := s.eng.AgentHistory(ctx, agentID)
		if err != nil {
			logging.Logger.Warn("mcp:tool:error", "name", "list_runs", "error", err)
			return mcp.NewToolResultError(fmt.Sprintf("list runs failed: %s", err)), nil
		}
		for i := range runs {
			runs[i].BeaconPath = filepath.Base(runs[i].BeaconPath)
		}
		return resultJSON(map[string]any{"agent_id": agentID, "runs": runs})
	}

	runs, err := s.eng.Runs(ctx)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "list_runs", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("list runs failed: %s", err)), nil
	}
	for _, r := range runs {
		r.BeaconPath = filepath.Base(r.BeaconPath)
	}

	return resultJSON(map[string]any{"runs": runs})
}

func (s *Server) handleListApprovals(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logging.Logger.Debug("mcp:tool", "name", "list_approvals")
	approvals, err := s.eng.Approvals(ctx)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "list_approvals", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("list approvals failed: %s", err)), nil
	}
	for _, a := range approvals {
		a.BeaconPath = filepath.Base(a.BeaconPath)
	}

	return resultJSON(map[string]any{"approvals": approvals})
}

func (s *Server) handleGetHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	resourceID, _ := args["resource_id"].(string)
	logging.Logger.Debug("mcp:tool", "name", "get_history", "resource_id", resourceID)
	if resourceID == "" {
		return mcp.NewToolResultError("resource_id is required"), nil
	}

	events, err := s.eng.History(ctx, resourceID)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "get_history", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("history failed: %s", err)), nil
	}
	scrubAuditEvents(events)

	return resultJSON(map[string]any{
		"resource_id": resourceID,
		"events":      events,
	})
}

func (s *Server) handleDiscoverBeacons(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	logging.Logger.Debug("mcp:tool", "name", "discover_beacons")
	beacons, err := s.eng.DiscoverBeacons()
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "discover_beacons", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("discover failed: %s", err)), nil
	}

	return resultJSON(map[string]any{"beacons": beacons})
}

func (s *Server) handleApply(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	beaconFile, _ := args["beacon_file"].(string)
	logging.Logger.Debug("mcp:tool", "name", "apply", "beacon_file", beaconFile)
	if beaconFile == "" {
		return mcp.NewToolResultError("beacon_file is required"), nil
	}
	if err := security.SafePath(s.eng.Root(), beaconFile); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "apply", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("invalid beacon_file: %s", err)), nil
	}

	var opts []engine.ApplyOption
	if force, ok := args["force"].(bool); ok {
		opts = append(opts, engine.WithForce(force))
	}
	if agentID, ok := args["agent_id"].(string); ok && agentID != "" {
		opts = append(opts, engine.WithAgentID(agentID, ""))
	}

	s.mu.Lock()
	if profile, ok := args["profile"].(string); ok && profile != "" {
		s.eng.ActiveProfile = profile
	}
	res, err := s.eng.Apply(ctx, beaconFile, opts...)
	s.mu.Unlock()

	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "apply", "error", err)
		// Return partial result if available (orphaned resource recovery).
		if res != nil {
			scrubApplyResult(res)
			return resultJSON(map[string]any{
				"error":          err.Error(),
				"partial_result": res,
			})
		}
		return mcp.NewToolResultError(fmt.Sprintf("apply failed: %s", err)), nil
	}

	scrubApplyResult(res)
	return resultJSON(res)
}

func (s *Server) handleApprove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	requestID, _ := args["request_id"].(string)
	logging.Logger.Debug("mcp:tool", "name", "approve", "request_id", requestID)
	if requestID == "" {
		return mcp.NewToolResultError("request_id is required"), nil
	}
	approver := "mcp-agent"
	if a, ok := args["approver"].(string); ok && a != "" {
		approver = a
	}

	res, err := s.eng.Approve(ctx, requestID, approver)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "approve", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("approve failed: %s", err)), nil
	}

	scrubApplyResult(res)
	return resultJSON(res)
}

func (s *Server) handleReject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	requestID, _ := args["request_id"].(string)
	logging.Logger.Debug("mcp:tool", "name", "reject", "request_id", requestID)
	if requestID == "" {
		return mcp.NewToolResultError("request_id is required"), nil
	}
	approver := "mcp-agent"
	if a, ok := args["approver"].(string); ok && a != "" {
		approver = a
	}
	reason := "rejected via MCP"
	if r, ok := args["reason"].(string); ok && r != "" {
		reason = r
	}

	if err := s.eng.Reject(ctx, requestID, approver, reason); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "reject", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("reject failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"rejected":   true,
		"request_id": requestID,
		"approver":   approver,
	})
}

func (s *Server) handleRollback(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	runID, _ := args["run_id"].(string)
	logging.Logger.Debug("mcp:tool", "name", "rollback", "run_id", runID)
	if runID == "" {
		return mcp.NewToolResultError("run_id is required"), nil
	}

	rollbackRunID, err := s.eng.Rollback(ctx, runID)
	if err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "rollback", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("rollback failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"original_run_id": runID,
		"rollback_run_id": rollbackRunID,
	})
}

func (s *Server) handleConnectProvider(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := req.GetArguments()
	prov, _ := args["provider"].(string)
	logging.Logger.Debug("mcp:tool", "name", "connect_provider", "provider", prov)
	if prov == "" {
		return mcp.NewToolResultError("provider is required"), nil
	}
	region, _ := args["region"].(string)

	if err := s.eng.Connect(ctx, prov, region); err != nil {
		logging.Logger.Warn("mcp:tool:error", "name", "connect_provider", "error", err)
		return mcp.NewToolResultError(fmt.Sprintf("connect failed: %s", err)), nil
	}

	return resultJSON(map[string]any{
		"status":   "connected",
		"provider": prov,
		"region":   region,
	})
}

// --- Helpers ---

// resultJSON serializes data as JSON text content in a CallToolResult.
func resultJSON(data any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("json marshal error: %s", err)), nil
	}
	return mcp.NewToolResultText(string(b)), nil
}

// scrubPlanResult removes sensitive data from a PlanResult before returning to agent.
func scrubPlanResult(res *engine.PlanResult) *engine.PlanResult {
	if res.Graph != nil {
		for i := range res.Graph.Nodes {
			res.Graph.Nodes[i].Intent = security.ScrubStringMap(res.Graph.Nodes[i].Intent)
			res.Graph.Nodes[i].Env = security.ScrubStringMap(res.Graph.Nodes[i].Env)
		}
	}
	if res.Plan != nil {
		for _, a := range res.Plan.Actions {
			a.Changes = security.ScrubChanges(a.Changes)
		}
	}
	if res.WiringResult != nil {
		res.WiringResult.InferredEnvVars = nil
	}
	return res
}

// scrubApplyResult removes sensitive data from an ApplyResult.
func scrubApplyResult(res *engine.ApplyResult) {
	if res == nil {
		return
	}
	for i := range res.Actions {
		if res.Actions[i].Action != nil {
			res.Actions[i].Action.Changes = security.ScrubChanges(res.Actions[i].Action.Changes)
		}
	}
}

// scrubState removes sensitive data from a full State snapshot.
// Matches the API server's scrubbing (internal/api/server.go:200-216).
func scrubState(st *state.State) {
	for _, rec := range st.Resources {
		rec.IntentSnapshot = security.ScrubMap(rec.IntentSnapshot)
		rec.LiveState = security.ScrubMap(rec.LiveState)
		if rec.Wiring != nil {
			rec.Wiring.InferredEnvVars = security.ScrubStringMap(rec.Wiring.InferredEnvVars)
		}
	}
	for _, a := range st.Actions {
		a.Changes = security.ScrubChanges(a.Changes)
	}
	for _, r := range st.Runs {
		r.BeaconPath = filepath.Base(r.BeaconPath)
	}
	for _, a := range st.Approvals {
		a.BeaconPath = filepath.Base(a.BeaconPath)
	}
	scrubAuditEvents(st.Audit)
}

// scrubAuditEvents removes sensitive data from audit event payloads.
func scrubAuditEvents(events []state.AuditEvent) {
	for i := range events {
		events[i].Data = security.ScrubMap(events[i].Data)
	}
}

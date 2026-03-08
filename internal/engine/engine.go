package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/terracotta-ai/beecon/internal/cloud"
	"github.com/terracotta-ai/beecon/internal/compliance"
	"github.com/terracotta-ai/beecon/internal/cost"
	"github.com/terracotta-ai/beecon/internal/discovery"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
	"github.com/terracotta-ai/beecon/internal/provider"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/state"
	"github.com/terracotta-ai/beecon/internal/wiring"
)

type Engine struct {
	store         *state.Store
	root          string
	exec          provider.Executor
	ActiveProfile string
	Force         bool
}

type PlanResult struct {
	Graph            *ir.Graph
	Plan             *resolver.Plan
	CloudProvider    string
	CloudRegion      string
	ComplianceReport *compliance.ComplianceReport
	CostReport       *cost.CostReport
	WiringResult     *wiring.WiringResult
}

type ActionStatus int

const (
	ActionExecuted  ActionStatus = iota
	ActionPending
	ActionForbidden
)

func (s ActionStatus) String() string {
	switch s {
	case ActionExecuted:
		return "executed"
	case ActionPending:
		return "pending"
	case ActionForbidden:
		return "forbidden"
	default:
		return "unknown"
	}
}

func (s ActionStatus) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

type ActionOutcome struct {
	Action *state.PlanAction `json:"action"`
	Status ActionStatus      `json:"status"`
}

type ApplyResult struct {
	RunID             string          `json:"run_id"`
	ApprovalRequestID string          `json:"approval_request_id,omitempty"`
	Executed          int             `json:"executed"`
	Pending           int             `json:"pending"`
	Actions           []ActionOutcome `json:"actions"`
	Simulated         bool            `json:"simulated"`
}

func New(rootDir string) *Engine {
	return &Engine{
		store: state.NewStore(rootDir),
		root:  rootDir,
		exec:  provider.NewExecutor(),
	}
}

func (e *Engine) IsSimulated() bool {
	return e.exec.IsDryRun()
}

func (e *Engine) DiscoverBeacons() ([]string, error) {
	return discovery.DiscoverBeacons(e.root)
}

func (e *Engine) Audit(ctx context.Context, resourceID string) ([]state.AuditEvent, error) {
	e.runExpireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	if resourceID == "" {
		out := append([]state.AuditEvent{}, st.Audit...)
		sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
		return out, nil
	}
	return e.History(ctx, resourceID)
}

func (e *Engine) Root() string { return e.root }

func (e *Engine) EnsureRoot() error {
	_, err := os.Stat(e.root)
	return err
}

func (e *Engine) Validate(beaconPath string) error {
	_, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	return err
}

func (e *Engine) Plan(ctx context.Context, beaconPath string) (*PlanResult, error) {
	e.runExpireApprovals()
	g, st, err := e.parseAndBuild(beaconPath)
	if err != nil {
		return nil, err
	}

	// Phase 3 pipeline: compliance → wiring → resolver → boundary → cost
	compReport, wiringResult, err := enrichGraph(g)
	if err != nil {
		return nil, err
	}
	p, err := resolver.BuildPlan(g, st)
	if err != nil {
		return nil, err
	}
	annotateBoundary(p, g.Domain)
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}

	var budget *cost.Budget
	if g.Domain != nil && g.Domain.Budget != "" {
		budget, err = cost.ParseBudget(g.Domain.Budget)
		if err != nil {
			return nil, fmt.Errorf("parse budget: %w", err)
		}
	}
	costReport := cost.Evaluate(p, g, st, budget)

	return &PlanResult{
		Graph:            g,
		Plan:             p,
		CloudProvider:    cloudProvider,
		CloudRegion:      cloudRegion,
		ComplianceReport: compReport,
		CostReport:       costReport,
		WiringResult:     wiringResult,
	}, nil
}

func (e *Engine) Apply(ctx context.Context, beaconPath string) (*ApplyResult, error) {
	abs, err := filepath.Abs(beaconPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", beaconPath, err)
	}
	g, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	if err != nil {
		return nil, err
	}

	// Phase 3 pipeline: compliance → wiring → (then resolver under tx)
	compReport, wiringResult, err := enrichGraph(g)
	if err != nil {
		return nil, err
	}

	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	p, err := resolver.BuildPlan(g, st)
	if err != nil {
		return nil, err
	}
	annotateBoundary(p, g.Domain)
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}

	// Budget check: block if over budget (unless --force)
	var budget *cost.Budget
	if g.Domain != nil && g.Domain.Budget != "" {
		budget, err = cost.ParseBudget(g.Domain.Budget)
		if err != nil {
			return nil, fmt.Errorf("parse budget: %w", err)
		}
	}
	costReport := cost.Evaluate(p, g, st, budget)
	if costReport.BudgetExceeded && !e.Force {
		return nil, fmt.Errorf("estimated cost $%.0f/mo exceeds budget $%.0f/mo (use --force to override)",
			costReport.TotalMonthlyCost, budget.MonthlyAmount())
	}

	// Compute intent hash for approval integrity.
	content, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read beacon for hash: %w", err)
	}
	intentHash := sha256hex(content)

	run := &state.RunRecord{
		ID:            state.NewID("run"),
		CreatedAt:     time.Now().UTC(),
		BeaconPath:    abs,
		Status:        state.RunApplied,
		ActiveProfile: e.ActiveProfile,
	}
	for _, a := range p.Actions {
		st.Actions[a.ID] = a
		run.ActionIDs = append(run.ActionIDs, a.ID)
	}

	pending := make([]*state.PlanAction, 0)
	executed := 0
	outcomes := make([]ActionOutcome, 0, len(p.Actions))
	for _, a := range p.Actions {
		if a.RequiresApproval {
			pending = append(pending, a)
			outcomes = append(outcomes, ActionOutcome{Action: a, Status: ActionPending})
			continue
		}
		if err := applyAction(ctx, e.exec, cloudProvider, cloudRegion, st, run.ID, a, g); err != nil {
			run.Status = state.RunFailed
			run.Error = err.Error()
			st.Runs[run.ID] = run
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("%w (also failed to save state: %v)", err, commitErr)
			}
			return nil, err
		}
		executed++
		run.ExecutedActions = append(run.ExecutedActions, a.ID)
		outcomes = append(outcomes, ActionOutcome{Action: a, Status: ActionExecuted})

		// Store wiring metadata and cost estimate on the resource record
		if rec := st.Resources[a.NodeID]; rec != nil {
			if wm := wiring.BuildMetadata(a.NodeID, wiringResult); wm != nil {
				rec.Wiring = &state.WiringMetadata{
					InferredEnvVars: wm.InferredEnvVars,
					InferredPolicy:  wm.InferredPolicy,
					InferredSGRules: wm.InferredSGRules,
				}
			}
			for _, est := range costReport.Estimates {
				if est.NodeID == a.NodeID {
					rec.EstimatedCost = est.MonthlyCost
					break
				}
			}
		}
	}

	var approvalID string
	if len(pending) > 0 {
		run.Status = state.RunPendingApproval
		req := &state.ApprovalRequest{
			ID:            state.NewID("apr"),
			CreatedAt:     time.Now().UTC(),
			RunID:         run.ID,
			Reason:        "boundary approve gate triggered",
			Status:        state.ApprovalPending,
			ExpiresAt:     time.Now().UTC().Add(24 * time.Hour),
			BeaconPath:    abs,
			IntentHash:    intentHash,
			CostDelta:     cost.FormatDelta(costReport),
			BlastRadius:   fmt.Sprintf("%d actions", len(pending)),
			ActiveProfile: e.ActiveProfile,
		}
		for _, a := range pending {
			req.ActionIDs = append(req.ActionIDs, a.ID)
			if rec := st.Resources[a.NodeID]; rec != nil {
				rec.ApprovalBlocked = true
			}
		}
		st.Approvals[req.ID] = req
		approvalID = req.ID
	}

	st.Runs[run.ID] = run
	addAudit(st, run.ID, "RUN_CREATED", "", fmt.Sprintf("run %s created for %s", run.ID, abs), nil)
	// Emit audit events for compliance overrides
	if compReport != nil {
		for _, o := range compReport.Overrides {
			addAudit(st, run.ID, "COMPLIANCE_OVERRIDE", o.NodeID,
				fmt.Sprintf("compliance override: field %q bypassed on %s", o.Field, o.NodeID),
				map[string]interface{}{"field": o.Field, "node_id": o.NodeID})
		}
	}
	if approvalID != "" {
		addAudit(st, run.ID, "APPROVAL_REQUIRED", "", fmt.Sprintf("approval %s required", approvalID), map[string]interface{}{"request_id": approvalID})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ApplyResult{
		RunID:             run.ID,
		ApprovalRequestID: approvalID,
		Executed:          executed,
		Pending:           len(pending),
		Actions:           outcomes,
		Simulated:         e.exec.IsDryRun(),
	}, nil
}

func (e *Engine) Approve(ctx context.Context, requestID, approver string) (*ApplyResult, error) {
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	req, ok := st.Approvals[requestID]
	if !ok {
		return nil, fmt.Errorf("approval request %s not found", requestID)
	}
	if req.Status != state.ApprovalPending {
		return nil, fmt.Errorf("approval request %s is %s", requestID, req.Status)
	}
	if time.Now().UTC().After(req.ExpiresAt) {
		return nil, fmt.Errorf("approval request %s expired at %s", requestID, req.ExpiresAt.Format(time.RFC3339))
	}

	// Approval integrity: verify the beacon file hasn't changed since Apply.
	if req.IntentHash != "" {
		content, readErr := os.ReadFile(req.BeaconPath)
		if readErr != nil {
			return nil, fmt.Errorf("read beacon for approval verification: %w", readErr)
		}
		if sha256hex(content) != req.IntentHash {
			return nil, fmt.Errorf("beacon file modified since apply; re-run apply")
		}
	}

	run, ok := st.Runs[req.RunID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", req.RunID)
	}
	g, err := parseAndBuildGraph(req.BeaconPath, req.ActiveProfile)
	if err != nil {
		return nil, err
	}
	// Enrich graph with compliance defaults and wiring (same pipeline as Apply).
	if _, _, enrichErr := enrichGraph(g); enrichErr != nil {
		return nil, fmt.Errorf("enrich graph for approval: %w", enrichErr)
	}
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}

	executed := 0
	outcomes := make([]ActionOutcome, 0, len(req.ActionIDs))
	for _, actionID := range req.ActionIDs {
		a, ok := st.Actions[actionID]
		if !ok {
			return nil, fmt.Errorf("action %s not found", actionID)
		}
		if err := applyAction(ctx, e.exec, cloudProvider, cloudRegion, st, run.ID, a, g); err != nil {
			run.Status = state.RunFailed
			run.Error = err.Error()
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("%w (also failed to save state: %v)", err, commitErr)
			}
			return nil, err
		}
		run.ExecutedActions = append(run.ExecutedActions, actionID)
		executed++
		outcomes = append(outcomes, ActionOutcome{Action: a, Status: ActionExecuted})
	}
	now := time.Now().UTC()
	req.Status = state.ApprovalApproved
	req.ResolvedBy = approver
	req.ResolvedAt = &now
	run.Status = state.RunApplied
	addAudit(st, run.ID, "APPROVAL_APPROVED", "", fmt.Sprintf("approval %s approved by %s", requestID, approver), nil)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ApplyResult{
		RunID:     run.ID,
		Executed:  executed,
		Actions:   outcomes,
		Simulated: e.exec.IsDryRun(),
	}, nil
}

func (e *Engine) Reject(ctx context.Context, requestID, approver, reason string) error {
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	req, ok := st.Approvals[requestID]
	if !ok {
		return fmt.Errorf("approval request %s not found", requestID)
	}
	if req.Status != state.ApprovalPending {
		return fmt.Errorf("approval request %s is %s", requestID, req.Status)
	}
	now := time.Now().UTC()
	req.Status = state.ApprovalRejected
	req.ResolvedBy = approver
	req.ResolvedAt = &now
	run := st.Runs[req.RunID]
	if run != nil {
		run.Status = state.RunFailed
		if strings.TrimSpace(reason) == "" {
			reason = "rejected by approver"
		}
		run.Error = reason
	}
	addAudit(st, req.RunID, "APPROVAL_REJECTED", "", fmt.Sprintf("approval %s rejected by %s: %s", requestID, approver, reason), nil)
	return tx.Commit()
}

func (e *Engine) Status(ctx context.Context) (*state.State, error) {
	e.runExpireApprovals()
	return e.store.Load()
}

func (e *Engine) Runs(ctx context.Context) ([]*state.RunRecord, error) {
	e.runExpireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	out := make([]*state.RunRecord, 0, len(st.Runs))
	for _, r := range st.Runs {
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (e *Engine) Approvals(ctx context.Context) ([]*state.ApprovalRequest, error) {
	e.runExpireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	out := make([]*state.ApprovalRequest, 0, len(st.Approvals))
	for _, r := range st.Approvals {
		cp := *r
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (e *Engine) Drift(ctx context.Context, beaconPath string) ([]*state.ResourceRecord, []error, error) {
	g, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	if err != nil {
		return nil, nil, err
	}
	// Enrich graph so intent hashes match what Apply stored.
	if _, _, enrichErr := enrichGraph(g); enrichErr != nil {
		return nil, nil, fmt.Errorf("enrich graph for drift: %w", enrichErr)
	}
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, nil, err
	}

	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	nodes := g.NodesByID()
	var drifted []*state.ResourceRecord
	var observeErrors []error
	changed := false
	for nodeID, rec := range st.Resources {
		if !rec.Managed {
			continue
		}
		n, ok := nodes[nodeID]
		if !ok {
			rec.Managed = false
			rec.Status = state.StatusObserved
			changed = true
			continue
		}
		obs, err := e.exec.Observe(ctx, cloudProvider, cloudRegion, rec)
		if err != nil {
			observeErrors = append(observeErrors, fmt.Errorf("observe %s: %w", nodeID, err))
			continue
		}
		if !obs.Exists {
			rec.Status = state.StatusDrifted
			rec.LiveState = map[string]interface{}{}
			rec.AgentReasoning = "resource missing from provider live state"
			trackDrift(rec)
			drifted = append(drifted, rec)
			changed = true
			continue
		}
		if obs.LiveState != nil {
			rec.LiveState = obs.LiveState
			changed = true
		}
		nowHash := state.HashMap(n.Snapshot())
		if nowHash != rec.IntentHash {
			rec.Status = state.StatusDrifted
			trackDrift(rec)
			drifted = append(drifted, rec)
			changed = true
		} else {
			rec.Status = state.StatusMatched
			clearDrift(rec)
		}
	}
	if changed {
		if err := tx.Commit(); err != nil {
			return nil, observeErrors, err
		}
	}
	sort.Slice(drifted, func(i, j int) bool { return drifted[i].ResourceID < drifted[j].ResourceID })
	return drifted, observeErrors, nil
}

// Refresh observes all managed resources and updates LiveState + LastSeen
// without comparing hashes or changing Status. Returns the count of refreshed
// resources and any observe errors.
func (e *Engine) Refresh(ctx context.Context, beaconPath string) (int, []error, error) {
	g, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	if err != nil {
		return 0, nil, err
	}
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return 0, nil, err
	}

	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	st := tx.State

	refreshed := 0
	var observeErrors []error
	for _, rec := range st.Resources {
		if !rec.Managed {
			continue
		}
		obs, err := e.exec.Observe(ctx, cloudProvider, cloudRegion, rec)
		if err != nil {
			observeErrors = append(observeErrors, fmt.Errorf("observe %s: %w", rec.ResourceID, err))
			continue
		}
		if obs.Exists {
			if obs.LiveState != nil {
				rec.LiveState = obs.LiveState
			}
			rec.LastSeen = time.Now().UTC()
			refreshed++
		}
	}

	if refreshed > 0 {
		if err := tx.Commit(); err != nil {
			return refreshed, observeErrors, err
		}
	}
	return refreshed, observeErrors, nil
}

// Import imports an existing cloud resource into beecon state by observing it.
// Returns the resource ID of the imported resource.
func (e *Engine) Import(ctx context.Context, providerName, resourceType, providerID, region string) (string, error) {
	// Build a synthetic ResourceRecord for observation
	nodeID := fmt.Sprintf("%s.%s", resourceType, providerID)
	rec := &state.ResourceRecord{
		ResourceID: nodeID,
		NodeType:   strings.ToUpper(resourceType),
		NodeName:   providerID,
		Provider:   strings.ToLower(providerName),
		ProviderID: providerID,
		Managed:    false,
	}

	obs, err := e.exec.Observe(ctx, strings.ToLower(providerName), region, rec)
	if err != nil {
		return "", fmt.Errorf("observe %s: %w", providerID, err)
	}
	if !obs.Exists {
		return "", fmt.Errorf("resource %s not found in %s/%s", providerID, providerName, region)
	}

	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	if existing, ok := st.Resources[nodeID]; ok && existing.Managed {
		return "", fmt.Errorf("resource %s already exists in state (status=%s)", nodeID, existing.Status)
	}

	rec.ProviderID = obs.ProviderID
	if rec.ProviderID == "" {
		rec.ProviderID = providerID
	}
	rec.ProviderRegion = region
	rec.LiveState = obs.LiveState
	rec.Managed = true
	rec.Status = state.StatusObserved
	rec.LastSeen = time.Now().UTC()
	rec.IntentSnapshot = map[string]interface{}{}
	rec.IntentHash = state.HashMap(rec.IntentSnapshot)
	rec.History = []string{fmt.Sprintf("%s IMPORTED", time.Now().UTC().Format(time.RFC3339))}

	st.Resources[nodeID] = rec
	addAudit(st, "", "RESOURCE_IMPORTED", nodeID,
		fmt.Sprintf("imported %s from %s/%s (provider_id=%s)", nodeID, providerName, region, rec.ProviderID), nil)

	if err := tx.Commit(); err != nil {
		return "", err
	}
	return nodeID, nil
}

func (e *Engine) History(ctx context.Context, resourceID string) ([]state.AuditEvent, error) {
	e.runExpireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	out := make([]state.AuditEvent, 0)
	for _, ev := range st.Audit {
		if ev.ResourceID == resourceID {
			out = append(out, ev)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

func (e *Engine) Rollback(ctx context.Context, runID string) (string, error) {
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	run, ok := st.Runs[runID]
	if !ok {
		return "", fmt.Errorf("run %s not found", runID)
	}
	if len(run.ExecutedActions) == 0 {
		return "", errors.New("run has no executed actions to rollback")
	}

	// Resolve cloud info for rollback cloud calls.
	var cloudProvider, cloudRegion string
	if run.BeaconPath != "" {
		if g, parseErr := parseAndBuildGraph(run.BeaconPath, run.ActiveProfile); parseErr == nil {
			cloudProvider, cloudRegion, _ = parseCloud(g)
		} else {
			addAudit(st, runID, "ROLLBACK_PARSE_WARNING", "", fmt.Sprintf("could not re-parse beacon: %v", parseErr), nil)
		}
	}

	rb := &state.RunRecord{
		ID:              state.NewID("run"),
		CreatedAt:       time.Now().UTC(),
		BeaconPath:      run.BeaconPath,
		Status:          state.RunRolledBack,
		RollbackOfRunID: runID,
	}
	for i := len(run.ExecutedActions) - 1; i >= 0; i-- {
		a := st.Actions[run.ExecutedActions[i]]
		if a == nil {
			continue
		}
		inverse := invertOperation(a.Operation)
		if err := e.applyInverse(ctx, cloudProvider, cloudRegion, st, rb.ID, a, inverse); err != nil {
			rb.Status = state.RunFailed
			rb.Error = err.Error()
			st.Runs[rb.ID] = rb
			if commitErr := tx.Commit(); commitErr != nil {
				return "", fmt.Errorf("%w (also failed to save state: %v)", err, commitErr)
			}
			return "", err
		}
		rb.ExecutedActions = append(rb.ExecutedActions, a.ID)
	}
	st.Runs[rb.ID] = rb
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return rb.ID, nil
}

func (e *Engine) Connect(ctx context.Context, providerName, region string) error {
	res, err := provider.Connect(ctx, providerName, region)
	if err != nil {
		return err
	}

	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	st.Connections[strings.ToLower(providerName)] = state.ProviderConnection{
		Provider:   strings.ToLower(providerName),
		Configured: true,
		Region:     res.Region,
		UpdatedAt:  time.Now().UTC(),
	}
	addAudit(st, "", "PROVIDER_CONNECTED", "", fmt.Sprintf("connected %s (%s) identity=%s", providerName, res.Region, maskIdentity(res.Identity)), nil)
	return tx.Commit()
}

func (e *Engine) IngestPerformanceBreach(ctx context.Context, resourceID, metric, observed, threshold, duration string) (string, error) {
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	ev := state.PerformanceEvent{
		ID:         state.NewID("perf"),
		Timestamp:  time.Now().UTC(),
		ResourceID: resourceID,
		Metric:     metric,
		Observed:   observed,
		Threshold:  threshold,
		Duration:   duration,
		Handled:    false,
	}
	st.PerfEvents = append(st.PerfEvents, ev)
	if len(st.PerfEvents) > maxPerfEvents {
		st.PerfEvents = st.PerfEvents[len(st.PerfEvents)-maxPerfEvents:]
	}
	addAudit(st, "", "PERFORMANCE_BREACH", resourceID, fmt.Sprintf("%s observed=%s threshold=%s duration=%s", metric, observed, threshold, duration), nil)
	if rec, ok := st.Resources[resourceID]; ok {
		rec.Status = state.StatusDrifted
		rec.AgentReasoning = "performance breach triggers resolver re-evaluation"
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return ev.ID, nil
}

func parseAndBuildGraph(beaconPath string, activeProfile string) (*ir.Graph, error) {
	f, err := parser.ParseFile(beaconPath)
	if err != nil {
		return nil, err
	}
	return ir.Build(f, beaconPath, activeProfile)
}

// enrichGraph runs the compliance and wiring pipeline on a parsed graph.
// This MUST be called on every code path that uses the graph for intent hashing,
// action execution, or drift comparison. Compliance and wiring mutate
// IntentNode.Intent and IntentNode.Env in-place, affecting the intent hash.
//
// Note: compliance.Enforce mutates the graph in-place (fills defaults). If wiring
// fails after compliance succeeds, the graph retains compliance mutations.
// This is acceptable because callers discard the graph on error.
func enrichGraph(g *ir.Graph) (*compliance.ComplianceReport, *wiring.WiringResult, error) {
	compReport, err := compliance.Enforce(g)
	if err != nil {
		return compReport, nil, err
	}
	wiringResult, err := wiring.WireGraph(g)
	if err != nil {
		return compReport, nil, err
	}
	return compReport, wiringResult, nil
}

func (e *Engine) parseAndBuild(beaconPath string) (*ir.Graph, *state.State, error) {
	g, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	if err != nil {
		return nil, nil, err
	}
	st, err := e.store.Load()
	if err != nil {
		return nil, nil, err
	}
	return g, st, nil
}

func annotateBoundary(p *resolver.Plan, d *ir.DomainNode) {
	if d == nil {
		return
	}
	for _, a := range p.Actions {
		tag := boundaryTagFor(a)
		if tag == "" {
			continue
		}
		a.BoundaryTag = tag
		if contains(d.Boundary["forbid"], tag) {
			a.RequiresApproval = false
			a.Reasoning = a.Reasoning + "; forbidden by boundary"
			a.Operation = "FORBIDDEN"
			continue
		}
		if contains(d.Boundary["approve"], tag) {
			a.RequiresApproval = true
		}
	}
}

func boundaryTagFor(a *state.PlanAction) string {
	switch {
	case a.Operation == "CREATE" && a.NodeType == "STORE":
		return "new_store"
	case a.Operation == "DELETE" && a.NodeType == "STORE":
		return "delete_store"
	case a.Operation == "UPDATE" && hasPrefixKey(a.Changes, "intent.instance_type"):
		return "instance_type_change"
	case a.Operation == "UPDATE" && hasPrefixKey(a.Changes, "intent.expose"):
		if strings.Contains(strings.ToLower(a.Changes["intent.expose"]), "public") {
			return "expose_public"
		}
	}
	return ""
}

func hasPrefixKey(m map[string]string, key string) bool {
	for k := range m {
		if k == key {
			return true
		}
	}
	return false
}

func contains(list []string, item string) bool {
	for _, v := range list {
		if v == item {
			return true
		}
	}
	return false
}

func applyAction(ctx context.Context, exec provider.Executor, cloudProvider, cloudRegion string, st *state.State, runID string, a *state.PlanAction, g *ir.Graph) error {
	if a.Operation == "FORBIDDEN" {
		return fmt.Errorf("action %s forbidden by boundary policy (%s)", a.ID, a.BoundaryTag)
	}
	nodeByID := g.NodesByID()
	n := nodeByID[a.NodeID]
	snap := n.Snapshot()
	hash := state.HashMap(snap)
	rec := st.Resources[a.NodeID]
	if a.Operation == "DELETE" && n.ID == "" && rec != nil && rec.IntentSnapshot != nil {
		snap = copyMap(rec.IntentSnapshot)
	}

	switch a.Operation {
	case "CREATE", "UPDATE":
		if n.ID == "" {
			return fmt.Errorf("action %s references unknown node %s", a.ID, a.NodeID)
		}
		if rec == nil {
			rec = &state.ResourceRecord{ResourceID: a.NodeID}
			st.Resources[a.NodeID] = rec
		}
		result, err := exec.Apply(ctx, provider.ApplyRequest{
			Provider: cloudProvider,
			Region:   cloudRegion,
			Action:   a,
			Intent:   snap,
			Record:   rec,
		})
		if err != nil {
			return err
		}
		rec.NodeType = a.NodeType
		rec.NodeName = a.NodeName
		rec.Provider = cloudProvider
		rec.ProviderRegion = cloudRegion
		rec.ProviderID = result.ProviderID
		rec.Managed = true
		rec.BeaconRef = n.Source
		rec.IntentSnapshot = copyMap(snap)
		rec.IntentHash = hash
		if result.LiveState != nil {
			rec.LiveState = result.LiveState
		} else {
			rec.LiveState = copyMap(snap)
		}
		rec.LastSeen = time.Now().UTC()
		rec.Status = state.StatusMatched
		clearDrift(rec)
		rec.AgentReasoning = a.Reasoning
		rec.LastAppliedRun = runID
		rec.LastOperation = a.Operation
		rec.ApprovalBlocked = false
		rec.Performance = n.Performance
		rec.History = append(rec.History, fmt.Sprintf("%s %s", time.Now().UTC().Format(time.RFC3339), a.Operation))
	case "DELETE":
		if rec == nil {
			return nil
		}
		_, err := exec.Apply(ctx, provider.ApplyRequest{
			Provider: cloudProvider,
			Region:   cloudRegion,
			Action:   a,
			Intent:   snap,
			Record:   rec,
		})
		if err != nil {
			return err
		}
		rec.Managed = false
		rec.Status = state.StatusObserved
		rec.LastAppliedRun = runID
		rec.LastOperation = a.Operation
		rec.History = append(rec.History, fmt.Sprintf("%s DELETE", time.Now().UTC().Format(time.RFC3339)))
	default:
		return fmt.Errorf("unsupported operation %s", a.Operation)
	}

	addAudit(st, runID, "ACTION_EXECUTED", a.NodeID, fmt.Sprintf("%s %s", a.Operation, a.NodeID), map[string]interface{}{"action_id": a.ID})
	return nil
}

func parseCloud(g *ir.Graph) (providerName, region string, err error) {
	if g.Domain == nil {
		return "", "", fmt.Errorf("missing domain block")
	}
	providerName, region, err = cloud.ParseSpec(g.Domain.Cloud)
	if err != nil {
		return "", "", err
	}
	return providerName, region, nil
}

// expireApprovalsInline is a pure function that mutates state in-place.
// It does not perform any I/O. Safe to call inside a transaction.
func expireApprovalsInline(st *state.State) {
	now := time.Now().UTC()
	for _, req := range st.Approvals {
		if req.Status != state.ApprovalPending {
			continue
		}
		if now.After(req.ExpiresAt) {
			req.Status = state.ApprovalRejected
			req.ResolvedBy = "system-timeout"
			req.ResolvedAt = &now
			if run := st.Runs[req.RunID]; run != nil {
				run.Status = state.RunFailed
				run.Error = "approval expired"
			}
			addAudit(st, req.RunID, "APPROVAL_EXPIRED", "", fmt.Sprintf("approval %s expired at %s", req.ID, req.ExpiresAt.Format(time.RFC3339)), nil)
		}
	}
}

// runExpireApprovals is used by read-only methods. It acquires a transaction
// internally so expiration is persisted atomically.
func (e *Engine) runExpireApprovals() {
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return
	}
	expireApprovalsInline(tx.State)
	_ = tx.Commit()
}

func (e *Engine) applyInverse(ctx context.Context, cloudProvider, cloudRegion string, st *state.State, runID string, a *state.PlanAction, inverse string) error {
	rec := st.Resources[a.NodeID]
	switch inverse {
	case "DELETE":
		// Rollback of CREATE: attempt cloud deletion.
		if rec != nil && cloudProvider != "" {
			deleteAction := &state.PlanAction{
				ID:        state.NewID("act"),
				NodeID:    a.NodeID,
				NodeType:  a.NodeType,
				NodeName:  a.NodeName,
				Operation: "DELETE",
			}
			if _, cloudErr := e.exec.Apply(ctx, provider.ApplyRequest{
				Provider: cloudProvider,
				Region:   cloudRegion,
				Action:   deleteAction,
				Intent:   copyMap(rec.IntentSnapshot),
				Record:   rec,
			}); cloudErr != nil {
				addAudit(st, runID, "ROLLBACK_CLOUD_ERROR", a.NodeID, cloudErr.Error(), nil)
			}
			rec.Managed = false
			rec.Status = state.StatusObserved
			rec.LastAppliedRun = runID
			rec.LastOperation = "ROLLBACK_DELETE"
			rec.History = append(rec.History, fmt.Sprintf("%s ROLLBACK_DELETE", time.Now().UTC().Format(time.RFC3339)))
		} else if rec != nil {
			rec.Managed = false
			rec.Status = state.StatusObserved
			rec.LastAppliedRun = runID
			rec.LastOperation = "ROLLBACK_DELETE"
			rec.History = append(rec.History, fmt.Sprintf("%s ROLLBACK_DELETE", time.Now().UTC().Format(time.RFC3339)))
		}
	case "RESTORE":
		if rec == nil {
			return fmt.Errorf("cannot restore %s: no prior record", a.NodeID)
		}
		// Rollback of DELETE: attempt cloud re-creation using stored snapshot.
		if cloudProvider != "" && rec.IntentSnapshot != nil {
			createAction := &state.PlanAction{
				ID:        state.NewID("act"),
				NodeID:    a.NodeID,
				NodeType:  a.NodeType,
				NodeName:  a.NodeName,
				Operation: "CREATE",
			}
			if _, cloudErr := e.exec.Apply(ctx, provider.ApplyRequest{
				Provider: cloudProvider,
				Region:   cloudRegion,
				Action:   createAction,
				Intent:   copyMap(rec.IntentSnapshot),
				Record:   rec,
			}); cloudErr != nil {
				addAudit(st, runID, "ROLLBACK_CLOUD_ERROR", a.NodeID, cloudErr.Error(), nil)
			}
		}
		rec.Managed = true
		rec.Status = state.StatusMatched
		rec.LastAppliedRun = runID
		rec.LastOperation = "ROLLBACK_RESTORE"
		rec.History = append(rec.History, fmt.Sprintf("%s ROLLBACK_RESTORE", time.Now().UTC().Format(time.RFC3339)))
	case "NOOP":
		return nil
	default:
		return fmt.Errorf("unsupported inverse operation %s", inverse)
	}
	addAudit(st, runID, "ACTION_ROLLED_BACK", a.NodeID, fmt.Sprintf("rollback %s via %s", a.ID, inverse), nil)
	return nil
}

func invertOperation(op string) string {
	switch op {
	case "CREATE":
		return "DELETE"
	case "UPDATE":
		return "NOOP"
	case "DELETE":
		return "RESTORE"
	default:
		return "NOOP"
	}
}

const maxAuditEvents = 10000
const maxPerfEvents = 10000

func addAudit(st *state.State, runID, typ, resourceID, msg string, data map[string]interface{}) {
	st.Audit = append(st.Audit, state.AuditEvent{
		ID:         state.NewID("aud"),
		Timestamp:  time.Now().UTC(),
		Type:       typ,
		ResourceID: resourceID,
		RunID:      runID,
		Message:    msg,
		Data:       data,
	})
	if len(st.Audit) > maxAuditEvents {
		st.Audit = st.Audit[len(st.Audit)-maxAuditEvents:]
	}
}

func sha256hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func copyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// trackDrift sets DriftFirstDetected on first detection and increments DriftCount.
func trackDrift(rec *state.ResourceRecord) {
	if rec.DriftFirstDetected == nil {
		now := time.Now().UTC()
		rec.DriftFirstDetected = &now
	}
	rec.DriftCount++
}

// clearDrift resets drift tracking when a resource is no longer drifted.
func clearDrift(rec *state.ResourceRecord) {
	rec.DriftFirstDetected = nil
	rec.DriftCount = 0
}

func maskIdentity(id string) string {
	if len(id) <= 4 {
		return "****"
	}
	return "****" + id[len(id)-4:]
}


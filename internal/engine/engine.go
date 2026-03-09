package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/terracotta-ai/beecon/internal/logging"

	"github.com/terracotta-ai/beecon/internal/cloud"
	"github.com/terracotta-ai/beecon/internal/compliance"
	"github.com/terracotta-ai/beecon/internal/cost"
	"github.com/terracotta-ai/beecon/internal/discovery"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
	"github.com/terracotta-ai/beecon/internal/provider"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
	"github.com/terracotta-ai/beecon/internal/wiring"
)

type Engine struct {
	store         *state.Store
	root          string
	exec          provider.Executor
	ActiveProfile string
}

type PlanResult struct {
	Graph            *ir.Graph                    `json:"graph,omitempty"`
	Plan             *resolver.Plan               `json:"plan"`
	CloudProvider    string                       `json:"cloud_provider"`
	CloudRegion      string                       `json:"cloud_region"`
	ComplianceReport *compliance.ComplianceReport `json:"compliance_report,omitempty"`
	CostReport       *cost.CostReport             `json:"cost_report,omitempty"`
	WiringResult     *wiring.WiringResult         `json:"wiring_result,omitempty"`
	Summary          *PlanSummary                 `json:"summary,omitempty"`
}

// PlanSummary provides an aggregate view of plan actions for agent decision-making.
type PlanSummary struct {
	TotalActions     int     `json:"total_actions"`
	Creates          int     `json:"creates"`
	Updates          int     `json:"updates"`
	Deletes          int     `json:"deletes"`
	Forbidden        int     `json:"forbidden"`
	PendingApproval  int     `json:"pending_approval"`
	AggregateRisk       string  `json:"aggregate_risk"`
	MaxBlastRadius      int     `json:"max_blast_radius"`
	MaxBlastRadiusLevel string  `json:"max_blast_radius_level"`
	TotalMonthlyCost    float64 `json:"total_monthly_cost"`
	CostDelta           float64 `json:"cost_delta,omitempty"`
	BudgetRemaining     float64 `json:"budget_remaining,omitempty"`
}

type ActionStatus int

const (
	ActionExecuted  ActionStatus = iota
	ActionPending
	ActionForbidden
	ActionSkipped
)

func (s ActionStatus) String() string {
	switch s {
	case ActionExecuted:
		return "executed"
	case ActionPending:
		return "pending"
	case ActionForbidden:
		return "forbidden"
	case ActionSkipped:
		return "skipped"
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

// ApplyOption configures Apply behavior.
type ApplyOption func(*applyOptions)

type applyOptions struct {
	Force bool
}

func applyDefaults(opts []ApplyOption) applyOptions {
	var o applyOptions
	for _, fn := range opts {
		fn(&o)
	}
	return o
}

// WithForce bypasses budget enforcement.
func WithForce(force bool) ApplyOption {
	return func(o *applyOptions) { o.Force = force }
}

type ApplyResult struct {
	RunID             string          `json:"run_id"`
	ApprovalRequestID string          `json:"approval_request_id,omitempty"`
	Executed          int             `json:"executed"`
	Pending           int             `json:"pending"`
	Skipped           int             `json:"skipped,omitempty"`
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
	logging.Logger.Debug("validate", "path", beaconPath, "profile", e.ActiveProfile)
	_, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	return err
}

func (e *Engine) Plan(ctx context.Context, beaconPath string) (*PlanResult, error) {
	logging.Logger.Debug("plan:start", "path", beaconPath, "profile", e.ActiveProfile)
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
	summary := enrichPlanActions(p, costReport, compReport)

	logging.Logger.Debug("plan:complete", "actions", len(p.Actions), "provider", cloudProvider, "region", cloudRegion)
	return &PlanResult{
		Graph:            g,
		Plan:             p,
		CloudProvider:    cloudProvider,
		CloudRegion:      cloudRegion,
		ComplianceReport: compReport,
		CostReport:       costReport,
		WiringResult:     wiringResult,
		Summary:          summary,
	}, nil
}

func (e *Engine) Apply(ctx context.Context, beaconPath string, opts ...ApplyOption) (*ApplyResult, error) {
	o := applyDefaults(opts)
	logging.Logger.Debug("apply:start", "path", beaconPath, "force", o.Force, "simulated", e.exec.IsDryRun())
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
	if costReport.BudgetExceeded && !o.Force {
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

	// Build completed set from any prior failed run for partial recovery.
	completedSet := findCompletedSetForBeacon(st, abs)

	pending := make([]*state.PlanAction, 0)
	executed := 0
	skipped := 0
	outcomes := make([]ActionOutcome, 0, len(p.Actions))
	for _, a := range p.Actions {
		if a.RequiresApproval {
			pending = append(pending, a)
			outcomes = append(outcomes, ActionOutcome{Action: a, Status: ActionPending})
			continue
		}

		// Idempotency check: skip actions that are already applied or completed.
		if skip, reason := isAlreadyApplied(a, st, completedSet); skip {
			logging.Logger.Debug("apply:idempotent-skip", "node", a.NodeName, "op", a.Operation, "reason", reason)
			skipped++
			run.CompletedActions = append(run.CompletedActions, completedActionKey(a))
			outcomes = append(outcomes, ActionOutcome{Action: a, Status: ActionSkipped})
			continue
		}

		if err := applyAction(ctx, e.exec, cloudProvider, cloudRegion, st, run.ID, a, g); err != nil {
			// Mark the resource as FAILED for CREATE operations so retries work.
			if a.Operation == "CREATE" {
				if rec := st.Resources[a.NodeID]; rec != nil && rec.ProviderID == "" {
					rec.Status = state.StatusFailed
				}
			}
			run.Status = state.RunFailed
			run.Error = err.Error()
			st.Runs[run.ID] = run
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("%w (also failed to save state: %v)", err, commitErr)
			}
			return &ApplyResult{
				RunID:     run.ID,
				Executed:  executed,
				Skipped:   skipped,
				Actions:   outcomes,
				Simulated: e.exec.IsDryRun(),
			}, err
		}
		executed++
		run.ExecutedActions = append(run.ExecutedActions, a.ID)
		run.CompletedActions = append(run.CompletedActions, completedActionKey(a))
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
	logging.Logger.Debug("apply:complete", "run", run.ID, "executed", executed, "skipped", skipped, "pending", len(pending))
	return &ApplyResult{
		RunID:             run.ID,
		ApprovalRequestID: approvalID,
		Executed:          executed,
		Pending:           len(pending),
		Skipped:           skipped,
		Actions:           outcomes,
		Simulated:         e.exec.IsDryRun(),
	}, nil
}

func (e *Engine) Approve(ctx context.Context, requestID, approver string) (*ApplyResult, error) {
	logging.Logger.Debug("approve:start", "request", requestID, "approver", approver)
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
	if run.Status != state.RunPendingApproval {
		return nil, fmt.Errorf("run %s has status %s, expected PENDING_APPROVAL", req.RunID, run.Status)
	}
	g, err := parseAndBuildGraph(req.BeaconPath, req.ActiveProfile)
	if err != nil {
		return nil, err
	}
	// Enrich graph with compliance defaults and wiring (same pipeline as Apply).
	compReport, wiringResult, enrichErr := enrichGraph(g)
	if enrichErr != nil {
		return nil, fmt.Errorf("enrich graph for approval: %w", enrichErr)
	}
	_ = compReport // used only for mutation side-effects
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}

	// Compute cost estimates for approved actions (same as Apply path).
	var budget *cost.Budget
	if g.Domain != nil && g.Domain.Budget != "" {
		budget, _ = cost.ParseBudget(g.Domain.Budget)
	}
	costReport := cost.Evaluate(&resolver.Plan{Actions: func() []*state.PlanAction {
		actions := make([]*state.PlanAction, 0, len(req.ActionIDs))
		for _, id := range req.ActionIDs {
			if a, ok := st.Actions[id]; ok {
				actions = append(actions, a)
			}
		}
		return actions
	}()}, g, st, budget)

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
			// Mark approval as failed and clear ApprovalBlocked on un-executed resources.
			now := time.Now().UTC()
			req.Status = state.ApprovalRejected
			req.ResolvedBy = "system"
			req.ResolvedAt = &now
			for _, aid := range req.ActionIDs {
				if act, ok := st.Actions[aid]; ok {
					if rec := st.Resources[act.NodeID]; rec != nil {
						rec.ApprovalBlocked = false
					}
				}
			}
			addAudit(st, run.ID, "APPROVAL_FAILED", "", fmt.Sprintf("approval %s failed during execution: %v", requestID, err), nil)
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("%w (also failed to save state: %v)", err, commitErr)
			}
			return &ApplyResult{
				RunID:     run.ID,
				Executed:  executed,
				Actions:   outcomes,
				Simulated: e.exec.IsDryRun(),
			}, err
		}
		run.ExecutedActions = append(run.ExecutedActions, actionID)
		executed++
		outcomes = append(outcomes, ActionOutcome{Action: a, Status: ActionExecuted})
		// Store wiring metadata and cost estimate (same as Apply path)
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
	// Clear ApprovalBlocked on resources affected by this rejection
	for _, actionID := range req.ActionIDs {
		if a, ok := st.Actions[actionID]; ok {
			if rec := st.Resources[a.NodeID]; rec != nil {
				rec.ApprovalBlocked = false
			}
		}
	}
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
	logging.Logger.Debug("drift:start", "path", beaconPath)
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
	changed := expireApprovalsInline(st)

	nodes := g.NodesByID()
	var drifted []*state.ResourceRecord
	var observeErrors []error
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

// ReconcileAction describes a single drift reconciliation action and its outcome.
type ReconcileAction struct {
	NodeName    string   `json:"node_name"`
	Target      string   `json:"target"`
	DriftFields []string `json:"drift_fields"`
	Status      string   `json:"status"` // "reconciled", "failed", "skipped"
	Error       string   `json:"error,omitempty"`
}

// ReconcileResult is the outcome of a drift reconciliation operation.
type ReconcileResult struct {
	DriftedCount      int               `json:"drifted_count"`
	ReconciledCount   int               `json:"reconciled_count"`
	FailedCount       int               `json:"failed_count"`
	ForbiddenCount    int               `json:"forbidden_count"`
	PendingApproval   int               `json:"pending_approval"`
	ApprovalRequestID string            `json:"approval_request_id,omitempty"`
	Actions           []ReconcileAction `json:"actions"`
}

// DriftReconcile detects drifted resources and generates (or executes) reconciliation
// actions to restore them to their intended state. If apply is true, the UPDATE
// actions are executed via the provider executor.
func (e *Engine) DriftReconcile(ctx context.Context, beaconPath string, apply bool) (*ReconcileResult, error) {
	logging.Logger.Debug("drift-reconcile:start", "path", beaconPath, "apply", apply)

	// Step 1: Run drift detection to get drifted resources.
	drifted, observeErrors, err := e.Drift(ctx, beaconPath)
	if err != nil {
		return nil, fmt.Errorf("drift detection failed: %w", err)
	}
	for _, oe := range observeErrors {
		logging.Logger.Warn("drift-reconcile: observe warning", "error", oe)
	}

	result := &ReconcileResult{
		DriftedCount: len(drifted),
		Actions:      make([]ReconcileAction, 0, len(drifted)),
	}

	if len(drifted) == 0 {
		return result, nil
	}

	// Step 2: Parse and build graph to get current intent for drifted resources.
	g, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	if err != nil {
		return nil, fmt.Errorf("parse beacon for reconcile: %w", err)
	}
	if _, _, enrichErr := enrichGraph(g); enrichErr != nil {
		return nil, fmt.Errorf("enrich graph for reconcile: %w", enrichErr)
	}
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}
	nodesByID := g.NodesByID()

	// Build a set of drifted resource IDs for quick lookup.
	driftedSet := make(map[string]*state.ResourceRecord, len(drifted))
	for _, rec := range drifted {
		driftedSet[rec.ResourceID] = rec
	}

	// Step 3: For each drifted resource, compute drift fields and generate a reconcile action.
	type pendingAction struct {
		reconcileIdx int
		planAction   *state.PlanAction
		node         ir.IntentNode
	}
	var pending []pendingAction

	for _, rec := range drifted {
		n, ok := nodesByID[rec.ResourceID]
		if !ok {
			// Resource removed from intent; skip reconciliation (would be a DELETE, not an UPDATE).
			result.Actions = append(result.Actions, ReconcileAction{
				NodeName:    rec.NodeName,
				Target:      rec.ResourceID,
				DriftFields: nil,
				Status:      "skipped",
				Error:       "resource no longer in intent",
			})
			continue
		}

		// Compute which fields drifted.
		intentSnap := n.Snapshot()
		driftFields := computeDriftFields(rec.IntentSnapshot, intentSnap, rec.LiveState)

		ra := ReconcileAction{
			NodeName:    rec.NodeName,
			Target:      rec.ResourceID,
			DriftFields: driftFields,
			Status:      "pending",
		}

		changes := reconcileDiff(rec.IntentSnapshot, intentSnap)
		// If intent hasn't changed but live state drifted, the changes map may be empty.
		// In that case, build changes from live state vs intent.
		if len(changes) == 0 {
			changes = reconcileDiff(rec.LiveState, intentSnap)
		}

		// Determine operation: if the resource was deleted from the cloud
		// (LiveState is empty), we need CREATE, not UPDATE.
		operation := "UPDATE"
		if len(rec.LiveState) == 0 {
			operation = "CREATE"
		}

		pa := &state.PlanAction{
			ID:        state.NewID("act"),
			NodeID:    rec.ResourceID,
			NodeType:  rec.NodeType,
			NodeName:  rec.NodeName,
			Operation: operation,
			Reasoning: "drift reconciliation: restoring to intended state",
			Changes:   changes,
		}

		result.Actions = append(result.Actions, ra)
		pending = append(pending, pendingAction{
			reconcileIdx: len(result.Actions) - 1,
			planAction:   pa,
			node:         n,
		})
	}

	// Step 3b: Annotate boundary policies (forbid/approve gates) on reconcile actions.
	// This mirrors the pattern in Engine.Apply to ensure reconciliation respects
	// the same boundary constraints as normal apply.
	var forbidden []pendingAction
	var gated []pendingAction   // actions requiring approval
	var allowed []pendingAction // actions that can execute immediately
	for _, p := range pending {
		tag := boundaryTagFor(p.planAction)
		if tag != "" {
			p.planAction.BoundaryTag = tag
			if g.Domain != nil && contains(g.Domain.Boundary["forbid"], tag) {
				p.planAction.Operation = "FORBIDDEN"
				p.planAction.RequiresApproval = false
				p.planAction.Reasoning += "; forbidden by boundary"
				result.Actions[p.reconcileIdx].Status = "forbidden"
				result.Actions[p.reconcileIdx].Error = fmt.Sprintf("forbidden by boundary policy (%s)", tag)
				result.ForbiddenCount++
				forbidden = append(forbidden, p)
				continue
			}
			if g.Domain != nil && contains(g.Domain.Boundary["approve"], tag) {
				p.planAction.RequiresApproval = true
			}
		}
		if p.planAction.RequiresApproval {
			gated = append(gated, p)
		} else {
			allowed = append(allowed, p)
		}
	}

	// If not applying, mark non-forbidden actions as "pending" (plan-only mode).
	if !apply {
		for _, p := range allowed {
			result.Actions[p.reconcileIdx].Status = "pending"
		}
		for _, p := range gated {
			result.Actions[p.reconcileIdx].Status = "pending_approval"
		}
		result.PendingApproval = len(gated)
		return result, nil
	}

	// Step 4: Execute reconciliation actions.
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		return nil, fmt.Errorf("load state for reconcile: %w", err)
	}
	defer tx.Rollback()
	st := tx.State
	expireApprovalsInline(st)

	runID := state.NewID("run")
	run := &state.RunRecord{
		ID:            runID,
		CreatedAt:     time.Now().UTC(),
		BeaconPath:    beaconPath,
		Status:        state.RunApplied,
		ActiveProfile: e.ActiveProfile,
	}

	// Register all actions (allowed + gated) in state.
	allExecutable := append(allowed, gated...)
	for _, p := range allExecutable {
		st.Actions[p.planAction.ID] = p.planAction
		run.ActionIDs = append(run.ActionIDs, p.planAction.ID)
	}

	// Execute only allowed (non-gated) actions.
	for _, p := range allowed {
		if err := applyAction(ctx, e.exec, cloudProvider, cloudRegion, st, runID, p.planAction, g); err != nil {
			result.Actions[p.reconcileIdx].Status = "failed"
			result.Actions[p.reconcileIdx].Error = err.Error()
			result.FailedCount++
			logging.Logger.Warn("drift-reconcile: action failed", "target", p.planAction.NodeID, "error", err)
			continue
		}

		result.Actions[p.reconcileIdx].Status = "reconciled"
		result.ReconciledCount++
		run.ExecutedActions = append(run.ExecutedActions, p.planAction.ID)
	}

	// Create approval request for gated actions (matching the Apply flow).
	if len(gated) > 0 {
		run.Status = state.RunPendingApproval

		// Compute intent hash for approval integrity.
		abs, _ := filepath.Abs(beaconPath)
		content, readErr := os.ReadFile(abs)
		intentHash := ""
		if readErr == nil {
			intentHash = sha256hex(content)
		}

		req := &state.ApprovalRequest{
			ID:            state.NewID("apr"),
			CreatedAt:     time.Now().UTC(),
			RunID:         runID,
			Reason:        "boundary approve gate triggered during drift reconciliation",
			Status:        state.ApprovalPending,
			ExpiresAt:     time.Now().UTC().Add(24 * time.Hour),
			BeaconPath:    abs,
			IntentHash:    intentHash,
			BlastRadius:   fmt.Sprintf("%d actions", len(gated)),
			ActiveProfile: e.ActiveProfile,
		}
		for _, p := range gated {
			req.ActionIDs = append(req.ActionIDs, p.planAction.ID)
			result.Actions[p.reconcileIdx].Status = "pending_approval"
			if rec := st.Resources[p.planAction.NodeID]; rec != nil {
				rec.ApprovalBlocked = true
			}
		}
		st.Approvals[req.ID] = req
		result.ApprovalRequestID = req.ID
		result.PendingApproval = len(gated)
		addAudit(st, runID, "APPROVAL_REQUIRED", "", fmt.Sprintf("reconcile approval %s required for %d actions", req.ID, len(gated)), map[string]interface{}{"request_id": req.ID})
	}

	if result.FailedCount > 0 && result.ReconciledCount == 0 && len(gated) == 0 {
		run.Status = state.RunFailed
		run.Error = "all reconciliation actions failed"
	} else if result.FailedCount > 0 && len(gated) == 0 {
		run.Status = state.RunFailed
		run.Error = fmt.Sprintf("%d of %d reconciliation actions failed", result.FailedCount, len(allowed))
	}

	st.Runs[runID] = run
	addAudit(st, runID, "DRIFT_RECONCILE", "", fmt.Sprintf("drift reconciliation: %d reconciled, %d failed, %d pending approval, %d forbidden", result.ReconciledCount, result.FailedCount, result.PendingApproval, result.ForbiddenCount), nil)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit reconcile state: %w", err)
	}

	logging.Logger.Debug("drift-reconcile:complete", "reconciled", result.ReconciledCount, "failed", result.FailedCount, "pending_approval", result.PendingApproval, "forbidden", result.ForbiddenCount)
	return result, nil
}

// computeDriftFields determines which fields differ between the stored intent,
// the current intent snapshot, and the live state.
func computeDriftFields(storedIntent, currentIntent, liveState map[string]interface{}) []string {
	seen := make(map[string]bool)
	var fields []string

	// Fields where live state differs from current intent.
	for k, intended := range currentIntent {
		live, ok := liveState[k]
		if !ok || fmt.Sprint(live) != fmt.Sprint(intended) {
			if !seen[k] {
				fields = append(fields, k)
				seen[k] = true
			}
		}
	}

	// Fields where stored intent differs from current intent (intent change caused drift).
	for k, current := range currentIntent {
		stored, ok := storedIntent[k]
		if !ok || fmt.Sprint(stored) != fmt.Sprint(current) {
			if !seen[k] {
				fields = append(fields, k)
				seen[k] = true
			}
		}
	}

	sort.Strings(fields)
	return fields
}

// reconcileDiff computes a changes map between two intent snapshots, suitable
// for populating PlanAction.Changes during reconciliation.
func reconcileDiff(old, new map[string]interface{}) map[string]string {
	changes := map[string]string{}
	for k, n := range new {
		if o, ok := old[k]; !ok || fmt.Sprint(o) != fmt.Sprint(n) {
			if security.IsSensitiveKey(k) {
				changes[k] = "**REDACTED** -> **REDACTED**"
			} else {
				changes[k] = fmt.Sprintf("%v -> %v", o, n)
			}
		}
	}
	for k, o := range old {
		if _, ok := new[k]; !ok {
			if security.IsSensitiveKey(k) {
				changes[k] = "**REDACTED** -> <deleted>"
			} else {
				changes[k] = fmt.Sprintf("%v -> <deleted>", o)
			}
		}
	}
	return changes
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
	if strings.TrimSpace(providerID) == "" {
		return "", fmt.Errorf("provider-id must not be empty")
	}
	// Extract a short name from the provider ID for the node ID.
	// AWS ARNs use colons and slashes; use the last segment as the name.
	shortName := providerID
	if idx := strings.LastIndex(shortName, "/"); idx >= 0 {
		shortName = shortName[idx+1:]
	} else if idx := strings.LastIndex(shortName, ":"); idx >= 0 {
		shortName = shortName[idx+1:]
	}
	if shortName == "" {
		shortName = providerID
	}
	nodeID := fmt.Sprintf("%s.%s", resourceType, shortName)
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
	rec.LastOperation = "IMPORT"
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
	if run.Status != state.RunApplied && run.Status != state.RunFailed {
		return "", fmt.Errorf("cannot rollback run %s: status is %s (must be APPLIED or FAILED)", runID, run.Status)
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
		ActiveProfile:   run.ActiveProfile,
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
	run.Status = state.RunRolledBack
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
	logging.Logger.Debug("parse:complete", "nodes", len(g.Nodes), "edges", len(g.Edges))
	st, err := e.store.Load()
	if err != nil {
		return nil, nil, err
	}
	logging.Logger.Debug("state:loaded", "resources", len(st.Resources), "runs", len(st.Runs))
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

// completedActionKey returns a unique key for tracking completed actions in a run.
// Uses NodeID (e.g. "store.cache") rather than NodeName (e.g. "cache") to avoid
// collisions when different node types share the same short name.
func completedActionKey(a *state.PlanAction) string {
	return a.NodeID + ":" + a.Operation
}

// isAlreadyApplied checks whether an action can be safely skipped because a
// previous apply already accomplished its effect. It returns true (skip) and a
// reason string, or false if the action should be executed.
func isAlreadyApplied(a *state.PlanAction, st *state.State, completedSet map[string]bool) (skip bool, reason string) {
	key := completedActionKey(a)

	// Check partial-failure recovery: if the action was already completed in a
	// prior run attempt, skip it.
	if completedSet[key] {
		return true, "skipping already-completed action from prior partial apply"
	}

	rec := st.Resources[a.NodeID]

	switch a.Operation {
	case "CREATE":
		if rec == nil {
			return false, ""
		}
		// If the resource previously failed, retry the CREATE.
		if rec.Status == state.StatusFailed {
			return false, ""
		}
		// If the resource has a ProviderID, it was already provisioned.
		if rec.ProviderID != "" {
			return true, "skipping idempotent CREATE: resource already provisioned"
		}
	case "DELETE":
		if rec == nil {
			return true, "skipping idempotent DELETE: resource already removed"
		}
		// If already marked as unmanaged/observed with no provider ID, skip.
		if !rec.Managed && rec.ProviderID == "" {
			return true, "skipping idempotent DELETE: resource already removed"
		}
		// If the last operation was DELETE, skip.
		if rec.LastOperation == "DELETE" && !rec.Managed {
			return true, "skipping idempotent DELETE: resource already removed"
		}
	}

	return false, ""
}

// buildCompletedSet creates a lookup set from a RunRecord's CompletedActions.
func buildCompletedSet(run *state.RunRecord) map[string]bool {
	s := make(map[string]bool, len(run.CompletedActions))
	for _, key := range run.CompletedActions {
		s[key] = true
	}
	return s
}

// findCompletedSetForBeacon scans state for the most recent FAILED run targeting
// the same beacon path and returns its completed actions as a set. This enables
// partial failure recovery: on retry, already-completed actions are skipped.
func findCompletedSetForBeacon(st *state.State, beaconPath string) map[string]bool {
	var latest *state.RunRecord
	for _, r := range st.Runs {
		if r.Status != state.RunFailed {
			continue
		}
		if r.BeaconPath != beaconPath {
			continue
		}
		if len(r.CompletedActions) == 0 {
			continue
		}
		if latest == nil || r.CreatedAt.After(latest.CreatedAt) {
			latest = r
		}
	}
	if latest == nil {
		return map[string]bool{}
	}
	return buildCompletedSet(latest)
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
// Returns true if any approvals were expired (state was mutated).
func expireApprovalsInline(st *state.State) bool {
	now := time.Now().UTC()
	mutated := false
	for _, req := range st.Approvals {
		if req.Status != state.ApprovalPending {
			continue
		}
		if now.After(req.ExpiresAt) {
			mutated = true
			req.Status = state.ApprovalRejected
			req.ResolvedBy = "system-timeout"
			req.ResolvedAt = &now
			if run := st.Runs[req.RunID]; run != nil {
				run.Status = state.RunFailed
				run.Error = "approval expired"
			}
			// Clear ApprovalBlocked on resources affected by expiration
			for _, actionID := range req.ActionIDs {
				if a, ok := st.Actions[actionID]; ok {
					if rec := st.Resources[a.NodeID]; rec != nil {
						rec.ApprovalBlocked = false
					}
				}
			}
			addAudit(st, req.RunID, "APPROVAL_EXPIRED", "", fmt.Sprintf("approval %s expired at %s", req.ID, req.ExpiresAt.Format(time.RFC3339)), nil)
		}
	}
	return mutated
}

// runExpireApprovals is used by read-only methods. It acquires a transaction
// internally so expiration is persisted atomically.
func (e *Engine) runExpireApprovals() {
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		logging.Logger.Warn("expire approvals: load failed", "error", err)
		return
	}
	if expireApprovalsInline(tx.State) {
		if err := tx.Commit(); err != nil {
			logging.Logger.Warn("expire approvals: commit failed", "error", err)
		}
	} else {
		tx.Rollback()
	}
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
		addAudit(st, runID, "ROLLBACK_SKIPPED", a.NodeID,
			fmt.Sprintf("rollback of %s %s skipped (UPDATE cannot be automatically reversed)", a.Operation, a.NodeID), nil)
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
	if in == nil {
		return nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		// Fallback to shallow copy if marshal fails (shouldn't happen with map[string]interface{})
		out := make(map[string]interface{}, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	var out map[string]interface{}
	if err := json.Unmarshal(b, &out); err != nil {
		out = make(map[string]interface{}, len(in))
		for k, v := range in {
			out[k] = v
		}
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

// enrichPlanActions annotates each PlanAction with cost, risk, rollback feasibility,
// and compliance mutation count, then builds an aggregate PlanSummary.
func enrichPlanActions(p *resolver.Plan, cr *cost.CostReport, compReport *compliance.ComplianceReport) *PlanSummary {
	if p == nil {
		return nil
	}

	// Build lookup maps for cost and compliance joins.
	costByNode := map[string]float64{}
	if cr != nil {
		for _, est := range cr.Estimates {
			costByNode[est.NodeID] = est.MonthlyCost
		}
	}
	mutationsByNode := map[string]int{}
	if compReport != nil {
		for _, m := range compReport.Mutations {
			mutationsByNode[m.NodeID]++
		}
	}

	summary := &PlanSummary{}
	highestRisk := 0

	for _, a := range p.Actions {
		// Cost per action.
		a.EstimatedCost = costByNode[a.NodeID]

		// Compliance mutations per action.
		a.ComplianceMutations = mutationsByNode[a.NodeID]

		// Risk scoring: operation type × node type.
		a.RiskScore, a.RiskLevel = scoreRisk(a.Operation, a.NodeType)
		if a.RiskScore > highestRisk {
			highestRisk = a.RiskScore
		}

		// Rollback feasibility.
		a.RollbackFeasibility = rollbackFeasibility(a.Operation, a.NodeType)

		// Aggregate summary counts.
		summary.TotalActions++
		switch a.Operation {
		case "CREATE":
			summary.Creates++
		case "UPDATE":
			summary.Updates++
		case "DELETE":
			summary.Deletes++
		case "FORBIDDEN":
			summary.Forbidden++
		}
		if a.RequiresApproval {
			summary.PendingApproval++
		}
	}

	// Blast radius scoring: must run after all RiskScores are set.
	highestBlast := 0
	for _, a := range p.Actions {
		a.BlastRadius, a.BlastRadiusLevel = scoreBlastRadius(a, p.Actions)
		if a.BlastRadius > highestBlast {
			highestBlast = a.BlastRadius
		}
	}

	summary.AggregateRisk = riskLevelFromScore(highestRisk)
	summary.MaxBlastRadius = highestBlast
	summary.MaxBlastRadiusLevel = riskLevelFromScore(highestBlast)
	if cr != nil {
		summary.TotalMonthlyCost = cr.TotalMonthlyCost
		if cr.Budget != nil {
			summary.BudgetRemaining = cr.Budget.MonthlyAmount() - cr.TotalMonthlyCost
		}
	}

	return summary
}

// scoreRisk returns a 1-10 risk score and level based on operation and node type.
func scoreRisk(operation, nodeType string) (int, string) {
	base := 0
	switch operation {
	case "CREATE":
		base = 2
	case "UPDATE":
		base = 4
	case "DELETE":
		base = 7
	case "FORBIDDEN":
		base = 1
	default:
		base = 1
	}

	// Data-bearing resources are higher risk.
	switch strings.ToLower(nodeType) {
	case "store":
		base += 2
	case "network":
		base += 1
	case "compute":
		base += 1
	case "service":
		// No additional risk.
	}

	if base > 10 {
		base = 10
	}
	return base, riskLevelFromScore(base)
}

func riskLevelFromScore(score int) string {
	switch {
	case score <= 2:
		return "low"
	case score <= 5:
		return "medium"
	case score <= 7:
		return "high"
	default:
		return "critical"
	}
}

// scoreBlastRadius computes a dependency-weighted blast radius score (1-10)
// for an action based on how many other actions depend on it, directly and
// transitively, and whether any downstream action is a DELETE or targets a
// STORE resource.
func scoreBlastRadius(action *state.PlanAction, allActions []*state.PlanAction) (int, string) {
	score := action.RiskScore

	// Build a map of nodeID → list of actions that depend on it.
	dependents := map[string][]*state.PlanAction{}
	for _, a := range allActions {
		for _, dep := range a.DependsOn {
			dependents[dep] = append(dependents[dep], a)
		}
	}

	// Collect all transitive dependents via BFS.
	visited := map[string]bool{}
	queue := []*state.PlanAction{}
	for _, d := range dependents[action.NodeID] {
		if !visited[d.NodeID] {
			visited[d.NodeID] = true
			queue = append(queue, d)
		}
	}
	directCount := len(queue)

	for i := 0; i < len(queue); i++ {
		cur := queue[i]
		for _, d := range dependents[cur.NodeID] {
			if !visited[d.NodeID] {
				visited[d.NodeID] = true
				queue = append(queue, d)
			}
		}
	}

	// +1 for every 2 direct dependents, capped at +3.
	depBonus := directCount / 2
	if depBonus > 3 {
		depBonus = 3
	}
	score += depBonus

	// Check downstream actions for cascading risk factors.
	hasDeleteDownstream := false
	hasStoreDownstream := false
	for _, d := range queue {
		if d.Operation == "DELETE" {
			hasDeleteDownstream = true
		}
		if strings.EqualFold(d.NodeType, "store") {
			hasStoreDownstream = true
		}
	}
	if hasDeleteDownstream {
		score++
	}
	if hasStoreDownstream {
		score++
	}

	if score > 10 {
		score = 10
	}
	return score, riskLevelFromScore(score)
}

// rollbackFeasibility assesses whether an action can be safely undone.
func rollbackFeasibility(operation, nodeType string) string {
	switch operation {
	case "CREATE":
		return "safe"
	case "UPDATE":
		// Store updates (schema changes, encryption) may be lossy.
		if strings.ToLower(nodeType) == "store" {
			return "risky"
		}
		return "safe"
	case "DELETE":
		// Deleting data stores means data loss.
		if strings.ToLower(nodeType) == "store" {
			return "impossible"
		}
		return "risky"
	default:
		return "safe"
	}
}


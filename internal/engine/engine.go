package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/terracotta-ai/beecon/internal/cloud"
	"github.com/terracotta-ai/beecon/internal/discovery"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
	"github.com/terracotta-ai/beecon/internal/provider"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/state"
)

type Engine struct {
	store *state.Store
	root  string
	exec  provider.Executor
}

type PlanResult struct {
	Graph *ir.Graph
	Plan  *resolver.Plan
}

type ApplyResult struct {
	RunID             string
	ApprovalRequestID string
	Executed          int
	Pending           int
}

func New(rootDir string) *Engine {
	return &Engine{
		store: state.NewStore(rootDir),
		root:  rootDir,
		exec:  provider.NewExecutor(),
	}
}

func (e *Engine) DiscoverBeacons() ([]string, error) {
	return discovery.DiscoverBeacons(e.root)
}

func (e *Engine) Audit(resourceID string) ([]state.AuditEvent, error) {
	st, err := e.store.Load()
	if err != nil {
		return nil, err
	}
	if resourceID == "" {
		out := append([]state.AuditEvent{}, st.Audit...)
		sort.Slice(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
		return out, nil
	}
	return e.History(resourceID)
}

func (e *Engine) Root() string { return e.root }

func (e *Engine) EnsureRoot() error {
	_, err := os.Stat(e.root)
	return err
}

func (e *Engine) Validate(beaconPath string) error {
	_, _, err := e.parseAndBuild(beaconPath)
	return err
}

func (e *Engine) Plan(beaconPath string) (*PlanResult, error) {
	_ = e.expireApprovals()
	g, st, err := e.parseAndBuild(beaconPath)
	if err != nil {
		return nil, err
	}
	p, err := resolver.BuildPlan(g, st)
	if err != nil {
		return nil, err
	}
	annotateBoundary(p, g.Domain)
	return &PlanResult{Graph: g, Plan: p}, nil
}

func (e *Engine) Apply(beaconPath string) (*ApplyResult, error) {
	_ = e.expireApprovals()
	abs, err := filepath.Abs(beaconPath)
	if err != nil {
		return nil, fmt.Errorf("resolve path %q: %w", beaconPath, err)
	}
	g, st, err := e.parseAndBuild(beaconPath)
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

	run := &state.RunRecord{
		ID:         state.NewID("run"),
		CreatedAt:  time.Now().UTC(),
		BeaconPath: abs,
		Status:     state.RunApplied,
	}
	for _, a := range p.Actions {
		st.Actions[a.ID] = a
		run.ActionIDs = append(run.ActionIDs, a.ID)
	}

	pending := make([]*state.PlanAction, 0)
	executed := 0
	for _, a := range p.Actions {
		if a.RequiresApproval {
			pending = append(pending, a)
			continue
		}
		if err := applyAction(context.Background(), e.exec, cloudProvider, cloudRegion, st, run.ID, a, g); err != nil {
			run.Status = state.RunFailed
			run.Error = err.Error()
			st.Runs[run.ID] = run
			if saveErr := e.store.Save(st); saveErr != nil {
				return nil, fmt.Errorf("%w (also failed to save state: %v)", err, saveErr)
			}
			return nil, err
		}
		executed++
		run.ExecutedActions = append(run.ExecutedActions, a.ID)
	}

	var approvalID string
	if len(pending) > 0 {
		run.Status = state.RunPendingApproval
		req := &state.ApprovalRequest{
			ID:          state.NewID("apr"),
			CreatedAt:   time.Now().UTC(),
			RunID:       run.ID,
			Reason:      "boundary approve gate triggered",
			Status:      state.ApprovalPending,
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
			BeaconPath:  abs,
			CostDelta:   estimateCostDelta(pending),
			BlastRadius: fmt.Sprintf("%d actions", len(pending)),
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
	if approvalID != "" {
		addAudit(st, run.ID, "APPROVAL_REQUIRED", "", fmt.Sprintf("approval %s required", approvalID), map[string]interface{}{"request_id": approvalID})
	}
	if err := e.store.Save(st); err != nil {
		return nil, err
	}
	return &ApplyResult{RunID: run.ID, ApprovalRequestID: approvalID, Executed: executed, Pending: len(pending)}, nil
}

func (e *Engine) Approve(requestID, approver string) (*ApplyResult, error) {
	_ = e.expireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return nil, err
	}
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
	run, ok := st.Runs[req.RunID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", req.RunID)
	}
	g, _, err := e.parseAndBuild(req.BeaconPath)
	if err != nil {
		return nil, err
	}
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}

	executed := 0
	for _, actionID := range req.ActionIDs {
		a, ok := st.Actions[actionID]
		if !ok {
			return nil, fmt.Errorf("action %s not found", actionID)
		}
		if err := applyAction(context.Background(), e.exec, cloudProvider, cloudRegion, st, run.ID, a, g); err != nil {
			run.Status = state.RunFailed
			run.Error = err.Error()
			if saveErr := e.store.Save(st); saveErr != nil {
				return nil, fmt.Errorf("%w (also failed to save state: %v)", err, saveErr)
			}
			return nil, err
		}
		run.ExecutedActions = append(run.ExecutedActions, actionID)
		executed++
	}
	now := time.Now().UTC()
	req.Status = state.ApprovalApproved
	req.ResolvedBy = approver
	req.ResolvedAt = &now
	run.Status = state.RunApplied
	addAudit(st, run.ID, "APPROVAL_APPROVED", "", fmt.Sprintf("approval %s approved by %s", requestID, approver), nil)
	if err := e.store.Save(st); err != nil {
		return nil, err
	}
	return &ApplyResult{RunID: run.ID, Executed: executed}, nil
}

func (e *Engine) Reject(requestID, approver, reason string) error {
	_ = e.expireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return err
	}
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
	return e.store.Save(st)
}

func (e *Engine) Status() (*state.State, error) {
	_ = e.expireApprovals()
	return e.store.Load()
}

func (e *Engine) Runs() ([]*state.RunRecord, error) {
	_ = e.expireApprovals()
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

func (e *Engine) Approvals() ([]*state.ApprovalRequest, error) {
	_ = e.expireApprovals()
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

func (e *Engine) Drift(beaconPath string) ([]*state.ResourceRecord, error) {
	_ = e.expireApprovals()
	g, st, err := e.parseAndBuild(beaconPath)
	if err != nil {
		return nil, err
	}
	cloudProvider, cloudRegion, err := parseCloud(g)
	if err != nil {
		return nil, err
	}
	nodes := g.NodesByID()
	var drifted []*state.ResourceRecord
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
		obs, err := e.exec.Observe(context.Background(), cloudProvider, cloudRegion, rec)
		if err != nil {
			return nil, err
		}
		if !obs.Exists {
			rec.Status = state.StatusDrifted
			rec.LiveState = map[string]interface{}{}
			rec.AgentReasoning = "resource missing from provider live state"
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
			drifted = append(drifted, rec)
			changed = true
		} else {
			rec.Status = state.StatusMatched
		}
	}
	if changed {
		if err := e.store.Save(st); err != nil {
			return nil, err
		}
	}
	sort.Slice(drifted, func(i, j int) bool { return drifted[i].ResourceID < drifted[j].ResourceID })
	return drifted, nil
}

func (e *Engine) History(resourceID string) ([]state.AuditEvent, error) {
	_ = e.expireApprovals()
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

func (e *Engine) Rollback(runID string) (string, error) {
	_ = e.expireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return "", err
	}
	run, ok := st.Runs[runID]
	if !ok {
		return "", fmt.Errorf("run %s not found", runID)
	}
	if len(run.ExecutedActions) == 0 {
		return "", errors.New("run has no executed actions to rollback")
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
		if err := applyInverse(st, rb.ID, a, inverse); err != nil {
			rb.Status = state.RunFailed
			rb.Error = err.Error()
			st.Runs[rb.ID] = rb
			if saveErr := e.store.Save(st); saveErr != nil {
				return "", fmt.Errorf("%w (also failed to save state: %v)", err, saveErr)
			}
			return "", err
		}
		rb.ExecutedActions = append(rb.ExecutedActions, a.ID)
	}
	st.Runs[rb.ID] = rb
	if err := e.store.Save(st); err != nil {
		return "", err
	}
	return rb.ID, nil
}

func (e *Engine) Connect(providerName, region string) error {
	_ = e.expireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return err
	}
	res, err := provider.Connect(context.Background(), providerName, region)
	if err != nil {
		return err
	}
	st.Connections[strings.ToLower(providerName)] = state.ProviderConnection{
		Provider:   strings.ToLower(providerName),
		Configured: true,
		Region:     res.Region,
		UpdatedAt:  time.Now().UTC(),
	}
	addAudit(st, "", "PROVIDER_CONNECTED", "", fmt.Sprintf("connected %s (%s) identity=%s", providerName, res.Region, res.Identity), nil)
	return e.store.Save(st)
}

func (e *Engine) IngestPerformanceBreach(resourceID, metric, observed, threshold, duration string) (string, error) {
	_ = e.expireApprovals()
	st, err := e.store.Load()
	if err != nil {
		return "", err
	}
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
	addAudit(st, "", "PERFORMANCE_BREACH", resourceID, fmt.Sprintf("%s observed=%s threshold=%s duration=%s", metric, observed, threshold, duration), nil)
	if rec, ok := st.Resources[resourceID]; ok {
		rec.Status = state.StatusDrifted
		rec.AgentReasoning = "performance breach triggers resolver re-evaluation"
	}
	if err := e.store.Save(st); err != nil {
		return "", err
	}
	return ev.ID, nil
}

func (e *Engine) parseAndBuild(beaconPath string) (*ir.Graph, *state.State, error) {
	f, err := parser.ParseFile(beaconPath)
	if err != nil {
		return nil, nil, err
	}
	g, err := ir.Build(f, beaconPath)
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
		rec.IntentSnapshot = scrubCredentials(snap)
		rec.IntentHash = hash
		if result.LiveState != nil {
			rec.LiveState = result.LiveState
		} else {
			rec.LiveState = copyMap(snap)
		}
		rec.LastSeen = time.Now().UTC()
		rec.Status = state.StatusMatched
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

func (e *Engine) expireApprovals() error {
	st, err := e.store.Load()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	changed := false
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
			changed = true
		}
	}
	if changed {
		return e.store.Save(st)
	}
	return nil
}

func applyInverse(st *state.State, runID string, a *state.PlanAction, inverse string) error {
	switch inverse {
	case "DELETE":
		rec := st.Resources[a.NodeID]
		if rec != nil {
			rec.Managed = false
			rec.Status = state.StatusObserved
			rec.LastAppliedRun = runID
			rec.LastOperation = "ROLLBACK_DELETE"
			rec.History = append(rec.History, fmt.Sprintf("%s ROLLBACK_DELETE", time.Now().UTC().Format(time.RFC3339)))
		}
	case "RESTORE":
		rec := st.Resources[a.NodeID]
		if rec == nil {
			return fmt.Errorf("cannot restore %s: no prior record", a.NodeID)
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
}

var sensitiveKeys = map[string]bool{
	"password":       true,
	"secret_value":   true,
	"token":          true,
	"admin_password": true,
	"secret":         true,
	"secret_key":     true,
}

func scrubCredentials(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		base := k
		if idx := strings.LastIndex(k, "."); idx >= 0 {
			base = k[idx+1:]
		}
		if sensitiveKeys[base] {
			out[k] = "**REDACTED**"
		} else {
			out[k] = v
		}
	}
	return out
}

func copyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func estimateCostDelta(actions []*state.PlanAction) string {
	if len(actions) == 0 {
		return "$0/mo"
	}
	var score int
	for _, a := range actions {
		if a.NodeType == "STORE" {
			score += 200
		} else {
			score += 40
		}
	}
	return fmt.Sprintf("+$%d/mo (estimated)", score)
}

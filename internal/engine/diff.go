package engine

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/logging"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// DiffResult describes the differences between a beacon file and the current state.
type DiffResult struct {
	Added     []DiffEntry `json:"added"`
	Removed   []DiffEntry `json:"removed"`
	Modified  []DiffEntry `json:"modified"`
	Unchanged int         `json:"unchanged"`
}

// DiffEntry describes a single resource difference.
type DiffEntry struct {
	NodeName string                 `json:"node_name"`
	NodeType string                 `json:"node_type"`
	Provider string                 `json:"provider,omitempty"`
	Target   string                 `json:"target,omitempty"`
	Changes  map[string]FieldDiff   `json:"changes,omitempty"`
}

// FieldDiff captures the old and new values of a changed field.
type FieldDiff struct {
	Old interface{} `json:"old"`
	New interface{} `json:"new"`
}

// Diff compares a beacon file against the current state without executing any
// changes. It returns a DiffResult describing added, removed, and modified
// resources. State is loaded read-only (no transaction needed).
func (e *Engine) Diff(ctx context.Context, beaconPath string) (*DiffResult, error) {
	logging.Logger.Debug("diff:start", "path", beaconPath, "profile", e.ActiveProfile)

	g, err := parseAndBuildGraph(beaconPath, e.ActiveProfile)
	if err != nil {
		return nil, fmt.Errorf("parse beacon: %w", err)
	}

	// Run the same enrichment pipeline (compliance + wiring) that Apply uses,
	// so that the beacon snapshot includes injected fields (env vars, inline_policy, etc.)
	// and matches what would be stored in state.
	if _, _, enrichErr := enrichGraph(g); enrichErr != nil {
		return nil, fmt.Errorf("enrich graph: %w", enrichErr)
	}

	st, err := e.store.Load()
	if err != nil {
		return nil, fmt.Errorf("load state: %w", err)
	}

	result := computeDiff(g, st)
	logging.Logger.Debug("diff:complete",
		"added", len(result.Added),
		"removed", len(result.Removed),
		"modified", len(result.Modified),
		"unchanged", result.Unchanged,
	)
	return result, nil
}

// computeDiff is the pure comparison function, separated from I/O for testability.
func computeDiff(g *ir.Graph, st *state.State) *DiffResult {
	result := &DiffResult{
		Added:    []DiffEntry{},
		Removed:  []DiffEntry{},
		Modified: []DiffEntry{},
	}

	// Build a set of node IDs from the beacon file for lookup.
	beaconNodeIDs := make(map[string]bool, len(g.Nodes))
	for _, node := range g.Nodes {
		beaconNodeIDs[node.ID] = true
	}

	// Build a set of managed resource IDs from state for lookup.
	stateNodeIDs := make(map[string]bool, len(st.Resources))
	for id, rec := range st.Resources {
		if rec.Managed {
			stateNodeIDs[id] = true
		}
	}

	// Detect added and modified resources.
	for _, node := range g.Nodes {
		snapshot := node.Snapshot()
		rec, exists := st.Resources[node.ID]

		if !exists || !rec.Managed {
			// Resource is in beacon but not in state -> Added.
			entry := diffEntryFromNode(node, g)
			result.Added = append(result.Added, entry)
			continue
		}

		// Resource exists in both -> compare intent snapshots.
		changes := compareSnapshots(rec.IntentSnapshot, snapshot)
		if len(changes) > 0 {
			entry := diffEntryFromNode(node, g)
			entry.Changes = changes
			result.Modified = append(result.Modified, entry)
		} else {
			result.Unchanged++
		}
	}

	// Detect removed resources: in state but not in beacon.
	// Collect and sort for deterministic output.
	var removedIDs []string
	for id := range st.Resources {
		rec := st.Resources[id]
		if rec.Managed && !beaconNodeIDs[id] {
			removedIDs = append(removedIDs, id)
		}
	}
	sort.Strings(removedIDs)
	for _, id := range removedIDs {
		rec := st.Resources[id]
		result.Removed = append(result.Removed, DiffEntry{
			NodeName: rec.NodeName,
			NodeType: rec.NodeType,
			Provider: rec.Provider,
		})
	}

	// Sort added and modified for deterministic output.
	sort.Slice(result.Added, func(i, j int) bool {
		return result.Added[i].NodeName < result.Added[j].NodeName
	})
	sort.Slice(result.Modified, func(i, j int) bool {
		return result.Modified[i].NodeName < result.Modified[j].NodeName
	})

	return result
}

// diffEntryFromNode creates a DiffEntry from an IR node, extracting provider/target
// from the node's intent fields and the graph's domain.
func diffEntryFromNode(node ir.IntentNode, g *ir.Graph) DiffEntry {
	entry := DiffEntry{
		NodeName: node.Name,
		NodeType: string(node.Type),
	}
	if g.Domain != nil {
		// Extract provider from domain cloud spec (e.g., "aws" from "aws(region: us-east-1)").
		entry.Provider = g.Domain.Cloud
	}
	if t, ok := node.Intent["target"]; ok {
		entry.Target = t
	}
	return entry
}

// compareSnapshots compares old (state) and new (beacon) intent snapshots,
// returning a map of fields that differ. Only intent-level fields are compared;
// ephemeral/computed fields (provider_id, live_state, etc.) are excluded
// because they exist only in state, not in beacon intent.
func compareSnapshots(old, new map[string]interface{}) map[string]FieldDiff {
	changes := map[string]FieldDiff{}

	// Check fields in new that differ from old or are new.
	for k, newVal := range new {
		oldVal, exists := old[k]
		if !exists {
			changes[k] = FieldDiff{Old: nil, New: newVal}
			continue
		}
		if !valuesEqual(oldVal, newVal) {
			changes[k] = FieldDiff{Old: oldVal, New: newVal}
		}
	}

	// Check fields in old that are missing from new (removed fields).
	for k, oldVal := range old {
		if _, exists := new[k]; !exists {
			changes[k] = FieldDiff{Old: oldVal, New: nil}
		}
	}

	return changes
}

// valuesEqual compares two interface{} values, normalizing numeric types
// to handle JSON round-trip differences (float64 vs int vs string).
func valuesEqual(a, b interface{}) bool {
	return reflect.DeepEqual(normalizeForDiff(a), normalizeForDiff(b))
}

// ScrubDiffResult replaces sensitive field values in a DiffResult with
// "**REDACTED**" so that passwords, API keys, etc. are never leaked in output.
func ScrubDiffResult(r *DiffResult) {
	for i := range r.Modified {
		for k, fd := range r.Modified[i].Changes {
			if security.IsSensitiveKey(k) {
				r.Modified[i].Changes[k] = FieldDiff{
					Old: redactIfNotNil(fd.Old),
					New: redactIfNotNil(fd.New),
				}
			}
		}
	}
}

func redactIfNotNil(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	return "**REDACTED**"
}

// normalizeForDiff converts float64 values that are whole numbers to int64
// so that JSON round-trips (which decode numbers as float64) don't cause
// phantom diffs. Mirrors state.normalizeValue.
func normalizeForDiff(v interface{}) interface{} {
	if f, ok := v.(float64); ok {
		if f == float64(int64(f)) {
			return int64(f)
		}
	}
	return v
}

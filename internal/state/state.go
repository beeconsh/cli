package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

var idCounter uint64

type ResourceStatus string

const (
	StatusMatched       ResourceStatus = "MATCHED"
	StatusDrifted       ResourceStatus = "DRIFTED"
	StatusUnprovisioned ResourceStatus = "UNPROVISIONED"
	StatusObserved      ResourceStatus = "OBSERVED"
)

type AuditEvent struct {
	ID         string                 `json:"id"`
	Timestamp  time.Time              `json:"timestamp"`
	Type       string                 `json:"type"`
	ResourceID string                 `json:"resource_id,omitempty"`
	RunID      string                 `json:"run_id,omitempty"`
	Message    string                 `json:"message"`
	Data       map[string]interface{} `json:"data,omitempty"`
}

type ResourceRecord struct {
	ResourceID      string                 `json:"resource_id"`
	NodeType        string                 `json:"node_type"`
	NodeName        string                 `json:"node_name"`
	Provider        string                 `json:"provider,omitempty"`
	ProviderRegion  string                 `json:"provider_region,omitempty"`
	ProviderID      string                 `json:"provider_id,omitempty"`
	Managed         bool                   `json:"managed"`
	BeaconRef       string                 `json:"beacon_ref"`
	IntentSnapshot  map[string]interface{} `json:"intent_snapshot"`
	IntentHash      string                 `json:"intent_hash"`
	LiveState       map[string]interface{} `json:"live_state"`
	History         []string               `json:"history"`
	AgentReasoning  string                 `json:"agent_reasoning"`
	Performance     map[string]string      `json:"performance"`
	LastSeen        time.Time              `json:"last_seen"`
	Status          ResourceStatus         `json:"status"`
	LastAppliedRun  string                 `json:"last_applied_run,omitempty"`
	LastOperation   string                 `json:"last_operation,omitempty"`
	ApprovalBlocked bool                   `json:"approval_blocked,omitempty"`
}

type PlanAction struct {
	ID               string            `json:"id"`
	NodeID           string            `json:"node_id"`
	NodeType         string            `json:"node_type"`
	NodeName         string            `json:"node_name"`
	Operation        string            `json:"operation"`
	DependsOn        []string          `json:"depends_on,omitempty"`
	Reasoning        string            `json:"reasoning"`
	Changes          map[string]string `json:"changes,omitempty"`
	BoundaryTag      string            `json:"boundary_tag,omitempty"`
	RequiresApproval bool              `json:"requires_approval"`
}

type RunStatus string

const (
	RunPendingApproval RunStatus = "PENDING_APPROVAL"
	RunApplied         RunStatus = "APPLIED"
	RunRolledBack      RunStatus = "ROLLED_BACK"
	RunFailed          RunStatus = "FAILED"
)

type RunRecord struct {
	ID              string    `json:"id"`
	CreatedAt       time.Time `json:"created_at"`
	BeaconPath      string    `json:"beacon_path"`
	ActionIDs       []string  `json:"action_ids"`
	ExecutedActions []string  `json:"executed_actions"`
	Status          RunStatus `json:"status"`
	Error           string    `json:"error,omitempty"`
	RollbackOfRunID string    `json:"rollback_of_run_id,omitempty"`
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "PENDING"
	ApprovalApproved ApprovalStatus = "APPROVED"
	ApprovalRejected ApprovalStatus = "REJECTED"
)

type ApprovalRequest struct {
	ID          string         `json:"id"`
	CreatedAt   time.Time      `json:"created_at"`
	RunID       string         `json:"run_id"`
	ActionIDs   []string       `json:"action_ids"`
	Reason      string         `json:"reason"`
	Status      ApprovalStatus `json:"status"`
	ResolvedBy  string         `json:"resolved_by,omitempty"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
	ExpiresAt   time.Time      `json:"expires_at"`
	BeaconPath  string         `json:"beacon_path"`
	CostDelta   string         `json:"cost_delta,omitempty"`
	BlastRadius string         `json:"blast_radius,omitempty"`
}

type ProviderConnection struct {
	Provider   string    `json:"provider"`
	Configured bool      `json:"configured"`
	Region     string    `json:"region,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PerformanceEvent struct {
	ID         string    `json:"id"`
	Timestamp  time.Time `json:"timestamp"`
	ResourceID string    `json:"resource_id"`
	Metric     string    `json:"metric"`
	Observed   string    `json:"observed"`
	Threshold  string    `json:"threshold"`
	Duration   string    `json:"duration"`
	Handled    bool      `json:"handled"`
}

type State struct {
	Version      int                           `json:"version"`
	Resources    map[string]*ResourceRecord    `json:"resources"`
	Audit        []AuditEvent                  `json:"audit"`
	Approvals    map[string]*ApprovalRequest   `json:"approvals"`
	Runs         map[string]*RunRecord         `json:"runs"`
	Actions      map[string]*PlanAction        `json:"actions"`
	Connections  map[string]ProviderConnection `json:"connections"`
	PerfEvents   []PerformanceEvent            `json:"performance_events"`
	LastModified time.Time                     `json:"last_modified"`
}

type Store struct {
	rootDir string
	path    string
	mu      sync.Mutex
}

func NewStore(rootDir string) *Store {
	stateDir := filepath.Join(rootDir, ".beecon")
	return &Store{rootDir: rootDir, path: filepath.Join(stateDir, "state.json")}
}

func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		return newState(), nil
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	if st.Resources == nil {
		st.Resources = map[string]*ResourceRecord{}
	}
	if st.Approvals == nil {
		st.Approvals = map[string]*ApprovalRequest{}
	}
	if st.Runs == nil {
		st.Runs = map[string]*RunRecord{}
	}
	if st.Actions == nil {
		st.Actions = map[string]*PlanAction{}
	}
	if st.Connections == nil {
		st.Connections = map[string]ProviderConnection{}
	}
	return &st, nil
}

func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	st.LastModified = time.Now().UTC()
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func newState() *State {
	return &State{
		Version:     1,
		Resources:   map[string]*ResourceRecord{},
		Audit:       []AuditEvent{},
		Approvals:   map[string]*ApprovalRequest{},
		Runs:        map[string]*RunRecord{},
		Actions:     map[string]*PlanAction{},
		Connections: map[string]ProviderConnection{},
		PerfEvents:  []PerformanceEvent{},
	}
}

func NewID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UTC().UnixNano(), atomic.AddUint64(&idCounter, 1))
}

func HashMap(m map[string]interface{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var s string
	for _, k := range keys {
		b, err := json.Marshal(m[k])
		if err != nil {
			s += fmt.Sprintf("%s=%v;", k, m[k])
		} else {
			s += fmt.Sprintf("%s=%s;", k, b)
		}
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

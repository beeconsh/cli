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
	"syscall"
	"time"

	"github.com/terracotta-ai/beecon/internal/logging"
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

// WiringMetadata tracks inferred wiring artifacts for a resource.
type WiringMetadata struct {
	InferredEnvVars map[string]string `json:"inferred_env_vars,omitempty"`
	InferredPolicy  string            `json:"inferred_policy,omitempty"`
	InferredSGRules []string          `json:"inferred_sg_rules,omitempty"`
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
	Wiring             *WiringMetadata        `json:"wiring,omitempty"`
	EstimatedCost      float64                `json:"estimated_cost,omitempty"`
	DriftFirstDetected *time.Time             `json:"drift_first_detected,omitempty"`
	DriftCount         int                    `json:"drift_count,omitempty"`
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

	// Phase 3: Plan enrichment fields for agent consumption.
	EstimatedCost       float64 `json:"estimated_cost,omitempty"`
	RiskScore           int     `json:"risk_score,omitempty"`
	RiskLevel           string  `json:"risk_level,omitempty"`
	RollbackFeasibility string  `json:"rollback_feasibility,omitempty"`
	ComplianceMutations int     `json:"compliance_mutations,omitempty"`
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
	ActiveProfile   string    `json:"active_profile,omitempty"`
}

type ApprovalStatus string

const (
	ApprovalPending  ApprovalStatus = "PENDING"
	ApprovalApproved ApprovalStatus = "APPROVED"
	ApprovalRejected ApprovalStatus = "REJECTED"
)

type ApprovalRequest struct {
	ID            string         `json:"id"`
	CreatedAt     time.Time      `json:"created_at"`
	RunID         string         `json:"run_id"`
	ActionIDs     []string       `json:"action_ids"`
	Reason        string         `json:"reason"`
	Status        ApprovalStatus `json:"status"`
	ResolvedBy    string         `json:"resolved_by,omitempty"`
	ResolvedAt    *time.Time     `json:"resolved_at,omitempty"`
	ExpiresAt     time.Time      `json:"expires_at"`
	BeaconPath    string         `json:"beacon_path"`
	IntentHash    string         `json:"intent_hash,omitempty"`
	CostDelta     string         `json:"cost_delta,omitempty"`
	BlastRadius   string         `json:"blast_radius,omitempty"`
	ActiveProfile string         `json:"active_profile,omitempty"`
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
	rootDir  string
	path     string
	lockPath string
	mu       sync.Mutex
}

func NewStore(rootDir string) *Store {
	stateDir := filepath.Join(rootDir, ".beecon")
	return &Store{
		rootDir:  rootDir,
		path:     filepath.Join(stateDir, "state.json"),
		lockPath: filepath.Join(stateDir, "state.lock"),
	}
}

func (s *Store) Load() (*State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.acquireFileLock()
	if err != nil {
		return nil, err
	}
	defer releaseFileLock(f)
	return s.loadLocked()
}

func (s *Store) Save(st *State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.acquireFileLock()
	if err != nil {
		return err
	}
	defer releaseFileLock(f)
	return s.saveLocked(st)
}

// StateTransaction holds the mutex and file lock for the duration of a
// read-modify-write cycle.
type StateTransaction struct {
	State    *State
	store    *Store
	lockFile *os.File
	done     bool
}

// LoadForUpdate acquires the store mutex and file lock, then loads state.
// The caller must call either Commit or Rollback to release both locks.
func (s *Store) LoadForUpdate() (*StateTransaction, error) {
	s.mu.Lock()
	f, err := s.acquireFileLock()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	st, err := s.loadLocked()
	if err != nil {
		releaseFileLock(f)
		s.mu.Unlock()
		return nil, err
	}
	if err := s.backupIfNeeded(); err != nil {
		logging.Logger.Warn("state backup failed, continuing with apply", "error", err)
	}
	return &StateTransaction{State: st, store: s, lockFile: f}, nil
}

// Commit saves the state and releases the file lock and mutex.
func (tx *StateTransaction) Commit() error {
	if tx.done {
		return nil
	}
	tx.done = true
	err := tx.store.saveLocked(tx.State)
	releaseFileLock(tx.lockFile)
	tx.store.mu.Unlock()
	return err
}

// Rollback releases the file lock and mutex without saving.
func (tx *StateTransaction) Rollback() {
	if tx.done {
		return
	}
	tx.done = true
	releaseFileLock(tx.lockFile)
	tx.store.mu.Unlock()
}

func (s *Store) acquireFileLock() (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(s.lockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire file lock: %w", err)
	}
	return f, nil
}

func releaseFileLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	f.Close()
}

func (s *Store) loadLocked() (*State, error) {
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
	if st.Version > CurrentVersion {
		return nil, fmt.Errorf("state.json version %d is newer than this beecon (supports up to %d); upgrade beecon", st.Version, CurrentVersion)
	}
	if st.Version < CurrentVersion {
		if err := runMigrations(&st); err != nil {
			return nil, fmt.Errorf("state migration failed: %w", err)
		}
	}
	return &st, nil
}

func (s *Store) saveLocked(st *State) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
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
		Version:     CurrentVersion,
		Resources:   map[string]*ResourceRecord{},
		Audit:       []AuditEvent{},
		Approvals:   map[string]*ApprovalRequest{},
		Runs:        map[string]*RunRecord{},
		Actions:     map[string]*PlanAction{},
		Connections: map[string]ProviderConnection{},
		PerfEvents:  []PerformanceEvent{},
	}
}

var pidSuffix = fmt.Sprintf("%d", os.Getpid())

func NewID(prefix string) string {
	return fmt.Sprintf("%s-%d-%s-%d", prefix, time.Now().UTC().UnixNano(), pidSuffix, atomic.AddUint64(&idCounter, 1))
}

func HashMap(m map[string]interface{}) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var s string
	for _, k := range keys {
		v := normalizeValue(m[k])
		b, err := json.Marshal(v)
		if err != nil {
			s += fmt.Sprintf("%s=%v;", k, v)
		} else {
			s += fmt.Sprintf("%s=%s;", k, b)
		}
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// normalizeValue converts float64 values that are whole numbers to int64
// so that JSON round-trips (which always decode numbers as float64) don't
// cause phantom hash diffs.
func normalizeValue(v interface{}) interface{} {
	if f, ok := v.(float64); ok {
		if f == float64(int64(f)) {
			return int64(f)
		}
	}
	return v
}

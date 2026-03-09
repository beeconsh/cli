package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

type Server struct {
	engine *engine.Engine
}

func New(e *engine.Engine) *Server {
	return &Server{engine: e}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/beacons", s.beacons)
	mux.HandleFunc("/api/beacon/validate", s.validateBeacon)
	mux.HandleFunc("/api/resolve", s.resolve)
	mux.HandleFunc("/api/graph", s.graph)
	mux.HandleFunc("/api/state", s.state)
	mux.HandleFunc("/api/runs", s.runs)
	mux.HandleFunc("/api/approvals", s.approvals)
	mux.HandleFunc("/api/audit", s.audit)
	mux.HandleFunc("/api/history", s.history)
	mux.HandleFunc("/api/drift", s.drift)
	mux.HandleFunc("/api/apply", s.apply)
	mux.HandleFunc("/api/approve", s.approve)
	mux.HandleFunc("/api/reject", s.reject)
	mux.HandleFunc("/api/connect", s.connect)
	mux.HandleFunc("/api/performance", s.performance)
	return apiKeyMiddleware(mux)
}

func apiKeyMiddleware(next http.Handler) http.Handler {
	key := os.Getenv("BEECON_API_KEY")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if key != "" {
			auth := r.Header.Get("Authorization")
			provided := strings.TrimPrefix(auth, "Bearer ")
			if !strings.HasPrefix(auth, "Bearer ") || subtle.ConstantTimeCompare([]byte(provided), []byte(key)) != 1 {
				log.Printf("auth: rejected request from %s", r.RemoteAddr)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing API key"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safePath(root, requested string) error {
	abs, err := filepath.Abs(filepath.Join(root, requested))
	if err != nil {
		return fmt.Errorf("invalid path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("invalid root")
	}
	if !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) && abs != absRoot {
		return fmt.Errorf("path escapes project root")
	}
	return nil
}

func (s *Server) beacons(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		paths, err := s.engine.DiscoverBeacons()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"beacons": paths})
	case http.MethodPost:
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if req.Path == "" {
			req.Path = "infra.beecon"
		}
		if err := safePath(s.engine.Root(), req.Path); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := s.engine.Validate(req.Path); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "registered", "path": req.Path})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) validateBeacon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Path == "" {
		req.Path = "infra.beecon"
	}
	if err := safePath(s.engine.Root(), req.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.engine.Validate(req.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "valid"})
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path  string `json:"path"`
		Apply bool   `json:"apply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Path == "" {
		req.Path = "infra.beecon"
	}
	if err := safePath(s.engine.Root(), req.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Apply {
		res, err := s.engine.Apply(r.Context(), req.Path)
		if err != nil {
			if res != nil {
				scrubOutcomes(res.Actions)
				writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
					"error":    err.Error(),
					"run_id":   res.RunID,
					"executed": res.Executed,
				})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		scrubOutcomes(res.Actions)
		writeJSON(w, http.StatusOK, res)
		return
	}
	res, err := s.engine.Plan(r.Context(), req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scrubActions(res.Plan.Actions)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"domain":  res.Graph.Domain.Name,
		"nodes":   len(res.Graph.Nodes),
		"actions": res.Plan.Actions,
	})
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	st, err := s.engine.Status(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
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
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	runs, err := s.engine.Runs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, run := range runs {
		run.BeaconPath = filepath.Base(run.BeaconPath)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"runs": runs})
}

func (s *Server) approvals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	approvals, err := s.engine.Approvals(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, a := range approvals {
		a.BeaconPath = filepath.Base(a.BeaconPath)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"approvals": approvals})
}

func (s *Server) graph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "infra.beecon"
	}
	if err := safePath(s.engine.Root(), path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := s.engine.Plan(r.Context(), path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scrubNodes(res.Graph.Nodes)
	scrubActions(res.Plan.Actions)
	domainSummary := map[string]string{"name": res.Graph.Domain.Name, "cloud": res.Graph.Domain.Cloud}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"domain":  domainSummary,
		"nodes":   res.Graph.Nodes,
		"edges":   res.Graph.Edges,
		"actions": res.Plan.Actions,
	})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resource := r.URL.Query().Get("resource")
	events, err := s.engine.Audit(r.Context(), resource)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scrubAuditEvents(events)
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resource := r.URL.Query().Get("resource")
	if resource == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resource query param required"})
		return
	}
	events, err := s.engine.History(r.Context(), resource)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scrubAuditEvents(events)
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) drift(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Path == "" {
		req.Path = "infra.beecon"
	}
	if err := safePath(s.engine.Root(), req.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	drifted, observeErrors, err := s.engine.Drift(r.Context(), req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	for _, rec := range drifted {
		rec.IntentSnapshot = security.ScrubMap(rec.IntentSnapshot)
		rec.LiveState = security.ScrubMap(rec.LiveState)
	}
	var warnings []string
	for _, e := range observeErrors {
		// Sanitize cloud error details — only expose the resource name and generic error
		msg := e.Error()
		// Strip common AWS patterns that leak account info
		if idx := strings.Index(msg, "arn:"); idx >= 0 {
			msg = msg[:idx] + "[ARN redacted]"
		}
		if idx := strings.Index(msg, "account"); idx >= 0 {
			end := idx + len("account")
			for end < len(msg) && msg[end] != ' ' && msg[end] != ',' && msg[end] != ')' {
				end++
			}
			msg = msg[:idx] + "[account redacted]" + msg[end:]
		}
		warnings = append(warnings, msg)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"drifted": drifted, "count": len(drifted), "warnings": warnings})
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		BeaconPath string `json:"beacon_path"`
		Force      bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.BeaconPath == "" {
		req.BeaconPath = "infra.beecon"
	}
	if err := safePath(s.engine.Root(), req.BeaconPath); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := s.engine.Apply(r.Context(), req.BeaconPath, engine.WithForce(req.Force))
	if err != nil {
		if res != nil {
			scrubOutcomes(res.Actions)
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
				"error":    err.Error(),
				"run_id":   res.RunID,
				"executed": res.Executed,
				"actions":  res.Actions,
			})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scrubOutcomes(res.Actions)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.RequestID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id required"})
		return
	}
	res, err := s.engine.Approve(r.Context(), req.RequestID, "api-user")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	scrubOutcomes(res.Actions)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
		Reason    string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.RequestID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id required"})
		return
	}
	if req.Reason == "" {
		req.Reason = "rejected by api-user"
	}
	if err := s.engine.Reject(r.Context(), req.RequestID, "api-user", req.Reason); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Region   string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Provider == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "provider required"})
		return
	}
	if err := s.engine.Connect(r.Context(), req.Provider, req.Region); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
}

func (s *Server) performance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, err := s.engine.Status(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"events": st.PerfEvents})
	case http.MethodPost:
		var req struct {
			ResourceID string `json:"resource_id"`
			Metric     string `json:"metric"`
			Observed   string `json:"observed"`
			Threshold  string `json:"threshold"`
			Duration   string `json:"duration"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if req.ResourceID == "" || req.Metric == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "resource_id and metric required"})
			return
		}
		if req.Duration == "" {
			req.Duration = "5m"
		}
		id, err := s.engine.IngestPerformanceBreach(r.Context(), req.ResourceID, req.Metric, req.Observed, req.Threshold, req.Duration)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"event_id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func scrubNodes(nodes []ir.IntentNode) {
	for i := range nodes {
		nodes[i].Intent = security.ScrubStringMap(nodes[i].Intent)
		nodes[i].Env = security.ScrubStringMap(nodes[i].Env)
	}
}

func scrubAuditEvents(events []state.AuditEvent) {
	for i := range events {
		events[i].Data = security.ScrubMap(events[i].Data)
	}
}

func scrubActions(actions []*state.PlanAction) {
	for _, a := range actions {
		a.Changes = security.ScrubChanges(a.Changes)
	}
}

func scrubOutcomes(outcomes []engine.ActionOutcome) {
	for _, ao := range outcomes {
		if ao.Action != nil {
			ao.Action.Changes = security.ScrubChanges(ao.Action.Changes)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
	}
}

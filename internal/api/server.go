package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/terracotta-ai/beecon/internal/engine"
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
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != key {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing API key"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safePath(root, requested string) (string, error) {
	abs, err := filepath.Abs(filepath.Join(root, requested))
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("invalid root")
	}
	if !strings.HasPrefix(abs, absRoot+string(filepath.Separator)) && abs != absRoot {
		return "", fmt.Errorf("path escapes project root")
	}
	return requested, nil
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
		if _, err := safePath(s.engine.Root(), req.Path); err != nil {
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
	if _, err := safePath(s.engine.Root(), req.Path); err != nil {
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
	if _, err := safePath(s.engine.Root(), req.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Apply {
		res, err := s.engine.Apply(req.Path)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	res, err := s.engine.Plan(req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
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
	st, err := s.engine.Status()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	for _, rec := range st.Resources {
		rec.IntentSnapshot = scrubMap(rec.IntentSnapshot)
		rec.LiveState = scrubMap(rec.LiveState)
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	runs, err := s.engine.Runs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"runs": runs})
}

func (s *Server) approvals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	approvals, err := s.engine.Approvals()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
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
	if _, err := safePath(s.engine.Root(), path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	res, err := s.engine.Plan(path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"domain":  res.Graph.Domain,
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
	events, err := s.engine.Audit(resource)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
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
	events, err := s.engine.History(resource)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
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
	if _, err := safePath(s.engine.Root(), req.Path); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	drifted, err := s.engine.Drift(req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"drifted": drifted, "count": len(drifted)})
}

func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
		Approver  string `json:"approver"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.RequestID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request_id required"})
		return
	}
	if req.Approver == "" {
		req.Approver = "api-user"
	}
	res, err := s.engine.Approve(req.RequestID, req.Approver)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) reject(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		RequestID string `json:"request_id"`
		Approver  string `json:"approver"`
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
	if req.Approver == "" {
		req.Approver = "api-user"
	}
	if req.Reason == "" {
		req.Reason = "rejected by api-user"
	}
	if err := s.engine.Reject(req.RequestID, req.Approver, req.Reason); err != nil {
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
	if err := s.engine.Connect(req.Provider, req.Region); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "connected"})
}

func (s *Server) performance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		st, err := s.engine.Status()
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
		id, err := s.engine.IngestPerformanceBreach(req.ResourceID, req.Metric, req.Observed, req.Threshold, req.Duration)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"event_id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

var apiSensitiveKeys = map[string]bool{
	"password":       true,
	"secret_value":   true,
	"token":          true,
	"admin_password": true,
	"secret":         true,
	"secret_key":     true,
}

func scrubMap(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		base := k
		if idx := strings.LastIndex(k, "."); idx >= 0 {
			base = k[idx+1:]
		}
		if apiSensitiveKeys[base] {
			out[k] = "**REDACTED**"
		} else {
			out[k] = v
		}
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
	}
}

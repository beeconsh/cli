package api

import (
	"encoding/json"
	"fmt"
	"net/http"

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
	return mux
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
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Path == "" {
			req.Path = "infra.beecon"
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
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Path == "" {
		req.Path = "infra.beecon"
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
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Path == "" {
		req.Path = "infra.beecon"
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
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Path == "" {
		req.Path = "infra.beecon"
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
	_ = json.NewDecoder(r.Body).Decode(&req)
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
	_ = json.NewDecoder(r.Body).Decode(&req)
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
	_ = json.NewDecoder(r.Body).Decode(&req)
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
		_ = json.NewDecoder(r.Body).Decode(&req)
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

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(w, `{"error":%q}`, err.Error())
	}
}

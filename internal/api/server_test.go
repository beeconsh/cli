package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/engine"
)

func TestAPIValidateAndPerformance(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
}
`
	if err := os.WriteFile(beacon, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	e := engine.New(dir)
	s := New(e)
	h := s.Handler()

	body, _ := json.Marshal(map[string]string{"path": "infra.beecon"})
	req := httptest.NewRequest(http.MethodPost, "/api/beacon/validate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("validate expected 200, got %d: %s", w.Code, w.Body.String())
	}

	_, err := e.Apply(context.Background(), "infra.beecon")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	perfBody, _ := json.Marshal(map[string]string{
		"resource_id": "service.api",
		"metric":      "latency_p95",
		"observed":    "380ms",
		"threshold":   "200ms",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/performance", bytes.NewReader(perfBody))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("performance post expected 200, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/performance", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("performance get expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAPIExtendedEndpoints(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
}
`
	if err := os.WriteFile(beacon, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	e := engine.New(dir)
	s := New(e)
	h := s.Handler()
	if _, err := e.Apply(context.Background(), "infra.beecon"); err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	cases := []struct {
		method string
		url    string
		body   []byte
		code   int
	}{
		{http.MethodGet, "/api/graph?path=infra.beecon", nil, http.StatusOK},
		{http.MethodGet, "/api/runs", nil, http.StatusOK},
		{http.MethodGet, "/api/approvals", nil, http.StatusOK},
		{http.MethodGet, "/api/audit", nil, http.StatusOK},
		{http.MethodPost, "/api/drift", []byte(`{"path":"infra.beecon"}`), http.StatusOK},
		{http.MethodGet, "/api/history?resource=service.api", nil, http.StatusOK},
		{http.MethodPost, "/api/connect", []byte(`{"provider":"unsupported","region":"x"}`), http.StatusBadRequest},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.url, bytes.NewReader(tc.body))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("%s %s expected %d got %d body=%s", tc.method, tc.url, tc.code, w.Code, w.Body.String())
		}
	}
}

func TestAPIRejectApproval(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  boundary {
    approve = [new_store]
  }
}
store postgres {
  engine = postgres
  username = admin
  password = secret123
}
`
	if err := os.WriteFile(beacon, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(cwd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	e := engine.New(dir)
	res, err := e.Apply(context.Background(), "infra.beecon")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.ApprovalRequestID == "" {
		t.Fatalf("expected approval request")
	}
	h := New(e).Handler()
	body := []byte(`{"request_id":"` + res.ApprovalRequestID + `","approver":"tester","reason":"no"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/reject", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reject expected 200 got %d body=%s", w.Code, w.Body.String())
	}
}

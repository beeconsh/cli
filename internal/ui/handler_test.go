package ui

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerDoesNotContainAPIKey(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if strings.Contains(body, "beecon-api-key") {
		t.Error("HTML should not contain beecon-api-key meta tag")
	}
	if w.Code != 200 {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestHandlerContainsLoginOverlay(t *testing.T) {
	h := Handler()
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body := w.Body.String()
	if !strings.Contains(body, "login-overlay") {
		t.Error("HTML should contain login overlay for auth prompt")
	}
	if !strings.Contains(body, "sessionStorage") {
		t.Error("HTML should use sessionStorage for API key")
	}
}

package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/auth"
)

func TestRegisterDisabledReturnsClearError(t *testing.T) {
	s := &Server{Auth: auth.New(nil), RegistrationMode: "disabled", Log: slog.Default()}
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", strings.NewReader(`{"email":"user@example.com","password":"secret1"}`))
	rec := httptest.NewRecorder()

	s.handleRegister(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "registration_disabled") {
		t.Fatalf("body lacks clear registration error: %s", rec.Body.String())
	}
}

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	s := &Server{Auth: auth.New(nil), CORSOrigins: []string{"https://app.example.com"}, Log: slog.Default()}
	h := s.Router()

	req := httptest.NewRequest(http.MethodOptions, "/v1/auth/login", nil)
	req.Header.Set("Origin", "https://app.example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example.com" {
		t.Fatalf("Allow-Origin = %q", got)
	}
	if allow := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(allow, "X-Workspace-ID") {
		t.Fatalf("Allow-Headers = %q", allow)
	}
	if allow := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(allow, "Idempotency-Key") {
		t.Fatalf("Allow-Headers missing Idempotency-Key = %q", allow)
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "" {
		t.Fatal("credentials must not be allowed")
	}
}

func TestCORSDisallowedOriginGetsNoHeaders(t *testing.T) {
	s := &Server{Auth: auth.New(nil), CORSOrigins: []string{"https://app.example.com"}, Log: slog.Default()}
	h := s.Router()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected Allow-Origin %q for disallowed origin", got)
	}
}

func TestCORSDisabledByDefault(t *testing.T) {
	s := &Server{Auth: auth.New(nil), Log: slog.Default()}
	h := s.Router()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://app.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("CORS should be off when unconfigured, got Allow-Origin %q", got)
	}
}

func TestVersionEndpointExposesAllCoordinates(t *testing.T) {
	prev_version := Version
	prev_revision := Revision
	prev_contract := ContractVersion
	t.Cleanup(func() {
		Version = prev_version
		Revision = prev_revision
		ContractVersion = prev_contract
	})
	Version = "0.2.0"
	Revision = "abcdef0123456789abcdef0123456789abcdef01"
	ContractVersion = "durable-context.v1"

	s := &Server{Auth: auth.New(nil), Log: slog.Default()}
	h := s.Router()

	req := httptest.NewRequest(http.MethodGet, "/v1/version", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["version"] != "0.2.0" {
		t.Errorf("version = %q, want %q", got["version"], "0.2.0")
	}
	if got["revision"] != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Errorf("revision = %q, want 40-hex commit", got["revision"])
	}
	if got["contract"] != "durable-context.v1" {
		t.Errorf("contract = %q, want %q", got["contract"], "durable-context.v1")
	}
}

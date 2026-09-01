package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAllChecksPassJSON(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("non-GET request: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/healthz":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !report.OK {
		t.Fatalf("expected OK, got summary=%q checks=%+v", report.Summary, report.Checks)
	}
	if report.SchemaVersion != doctorSchemaVersion {
		t.Errorf("schema_version = %q, want %q", report.SchemaVersion, doctorSchemaVersion)
	}

	checkNames := make(map[string]bool)
	for _, c := range report.Checks {
		checkNames[c.Name] = true
		if c.Code == "" {
			t.Errorf("check %q has empty code", c.Name)
		}
	}
	for _, want := range []string{"config", "server", "credentials", "workspace", "version"} {
		if !checkNames[want] {
			t.Errorf("missing check %q", want)
		}
	}
}

func TestDoctorAllChecksPassText(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	out := stdout.String()
	for _, want := range []string{"[ok] config", "[ok] server", "[ok] credentials", "[ok] workspace"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "all checks passed") {
		t.Errorf("text output missing summary:\n%s", out)
	}
}

func TestDoctorAlwaysExitsZero(t *testing.T) {
	clearCLIOverrides(t)
	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", "http://127.0.0.1:1")
	t.Setenv("MEM_TOKEN", "")
	t.Setenv("MEM_WORKSPACE", "")

	root := newRootCmd()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	err := root.Execute()
	if err != nil {
		t.Fatalf("doctor should always exit 0, got error: %v", err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.OK {
		t.Fatal("expected report.OK=false when checks fail")
	}
}

func TestDoctorServerUnreachable(t *testing.T) {
	clearCLIOverrides(t)
	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", "http://127.0.0.1:1")
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	var serverCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "server" {
			serverCheck = &report.Checks[i]
			break
		}
	}
	if serverCheck == nil {
		t.Fatal("missing server check")
	}
	if serverCheck.Status != "fail" {
		t.Errorf("server check status = %q, want fail", serverCheck.Status)
	}
	if serverCheck.Code != "server_unreachable" {
		t.Errorf("server check code = %q, want server_unreachable", serverCheck.Code)
	}
	if !strings.Contains(serverCheck.Hint, "deploy/compose") {
		t.Errorf("server check hint should name deploy/compose path, got %q", serverCheck.Hint)
	}
	if strings.Contains(serverCheck.Message, "127.0.0.1") {
		t.Error("server check message leaks raw URL")
	}
}

func TestDoctorNoCredentials(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	var credCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "credentials" {
			credCheck = &report.Checks[i]
			break
		}
	}
	if credCheck == nil {
		t.Fatal("missing credentials check")
	}
	if credCheck.Status != "fail" {
		t.Errorf("credentials check status = %q, want fail", credCheck.Status)
	}
	if credCheck.Code != "no_credential" {
		t.Errorf("credentials check code = %q, want no_credential", credCheck.Code)
	}
	if !strings.Contains(credCheck.Hint, "mem auth login") {
		t.Errorf("credentials check should hint at auth login, got %q", credCheck.Hint)
	}
	if !strings.Contains(credCheck.Hint, "deploy/compose") {
		t.Errorf("credentials check should name deploy/compose path, got %q", credCheck.Hint)
	}
}

func TestDoctorNoWorkspace(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	var wsCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "workspace" {
			wsCheck = &report.Checks[i]
			break
		}
	}
	if wsCheck == nil {
		t.Fatal("missing workspace check")
	}
	if wsCheck.Status != "warn" {
		t.Errorf("workspace check status = %q, want warn", wsCheck.Status)
	}
	if wsCheck.Code != "no_workspace" {
		t.Errorf("workspace check code = %q, want no_workspace", wsCheck.Code)
	}
}

func TestDoctorVersionSkew(t *testing.T) {
	clearCLIOverrides(t)
	oldVersion := cliVersion
	cliVersion = "1.0.0"
	t.Cleanup(func() { cliVersion = oldVersion })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"2.0.0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	var verCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "version" {
			verCheck = &report.Checks[i]
			break
		}
	}
	if verCheck == nil {
		t.Fatal("missing version check")
	}
	if verCheck.Status != "warn" {
		t.Errorf("version check status = %q, want warn", verCheck.Status)
	}
	if verCheck.Code != "version_skew" {
		t.Errorf("version check code = %q, want version_skew", verCheck.Code)
	}
	if !strings.Contains(verCheck.Message, "1.0.0") || !strings.Contains(verCheck.Message, "2.0.0") {
		t.Errorf("version check message should mention both versions, got %q", verCheck.Message)
	}
}

func TestDoctorMalformedServerURL(t *testing.T) {
	clearCLIOverrides(t)
	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", "://not-a-url")
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	output := stdout.String()
	if strings.Contains(output, "test-token") {
		t.Error("output leaks token value")
	}
	if strings.Contains(output, "://not-a-url") {
		t.Error("output leaks raw malformed URL")
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.OK {
		t.Fatal("expected report.OK=false for malformed URL")
	}
}

func TestDoctorServerReturnsNon200Healthz(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal error`))
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	var serverCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "server" {
			serverCheck = &report.Checks[i]
			break
		}
	}
	if serverCheck == nil {
		t.Fatal("missing server check")
	}
	if serverCheck.Status != "fail" {
		t.Errorf("server check status = %q, want fail for 500 response", serverCheck.Status)
	}
	if strings.Contains(serverCheck.Message, "internal error") {
		t.Error("server check message leaks raw server error body")
	}
}

func TestDoctorServerReturnsNonJSONVersion(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(`not json`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}

	var verCheck *doctorCheck
	for i := range report.Checks {
		if report.Checks[i].Name == "version" {
			verCheck = &report.Checks[i]
			break
		}
	}
	if verCheck == nil {
		t.Fatal("missing version check")
	}
	if verCheck.Status != "warn" {
		t.Errorf("version check status = %q, want warn for non-JSON response", verCheck.Status)
	}
	if strings.Contains(verCheck.Message, "not json") {
		t.Error("version check message leaks raw server response body")
	}
}

func TestDoctorDevBuildSkipsVersionSkew(t *testing.T) {
	clearCLIOverrides(t)
	oldVersion := cliVersion
	cliVersion = "dev"
	t.Cleanup(func() { cliVersion = oldVersion })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"3.5.0"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if !report.OK {
		t.Fatalf("dev build should not fail on version skew: %+v", report.Checks)
	}
}

func TestDoctorNoSecretsInTextOutput(t *testing.T) {
	clearCLIOverrides(t)
	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", "http://127.0.0.1:1")
	t.Setenv("MEM_TOKEN", "super-secret-token-xyz")
	t.Setenv("MEM_WORKSPACE", "ws-secret")

	root := newRootCmd()
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor"})
	_ = root.Execute()

	out := stdout.String()
	if strings.Contains(out, "super-secret-token-xyz") {
		t.Error("text output leaks token value")
	}
}

func TestDoctorInvalidFormatFlag(t *testing.T) {
	clearCLIOverrides(t)
	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))

	root := newRootCmd()
	root.SetArgs([]string{"doctor", "--format", "xml"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --format")
	}
}

// AC-002: prove the doctor transport only issues GET requests.
type writeFailingTransport struct {
	t *testing.T
}

func (s *writeFailingTransport) Do(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		s.t.Fatalf("doctor issued non-GET request: %s %s", req.Method, req.URL.Path)
	}
	rec := httptest.NewRecorder()
	switch req.URL.Path {
	case "/healthz":
		rec.WriteHeader(http.StatusOK)
		_, _ = rec.Write([]byte(`{"ok":true}`))
	case "/v1/version":
		rec.WriteHeader(http.StatusOK)
		_, _ = rec.Write([]byte(`{"version":"dev"}`))
	default:
		rec.WriteHeader(http.StatusNotFound)
	}
	return rec.Result(), nil
}

func TestDoctorPerformsNoWriteRequests(t *testing.T) {
	clearCLIOverrides(t)
	oldClient := doctorHTTPClient
	doctorHTTPClient = &writeFailingTransport{t: t}
	t.Cleanup(func() { doctorHTTPClient = oldClient })

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", "http://doctor-test.local")
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}

// AC-003: golden file for JSON output shape.
func TestDoctorJSONMatchesGoldenFile(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "test-token")
	t.Setenv("MEM_WORKSPACE", "ws-123")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v", err)
	}

	if report.SchemaVersion != "mem.doctor/v1" {
		t.Errorf("schema_version = %q, want mem.doctor/v1", report.SchemaVersion)
	}

	expectedChecks := []struct {
		name string
		code string
	}{
		{"config", "config_ok"},
		{"server", "server_reachable"},
		{"credentials", "credential_present"},
		{"workspace", "workspace_selected"},
		{"version", "version_match"},
	}
	if len(report.Checks) != len(expectedChecks) {
		t.Fatalf("got %d checks, want %d", len(report.Checks), len(expectedChecks))
	}
	for i, want := range expectedChecks {
		if report.Checks[i].Name != want.name {
			t.Errorf("check[%d].Name = %q, want %q", i, report.Checks[i].Name, want.name)
		}
		if report.Checks[i].Code != want.code {
			t.Errorf("check[%d].Code = %q, want %q", i, report.Checks[i].Code, want.code)
		}
		if report.Checks[i].Status != "ok" {
			t.Errorf("check[%d].Status = %q, want ok", i, report.Checks[i].Status)
		}
	}

	goldenPath := filepath.Join("testdata", "doctor-golden.json")
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden file: %v (run with -update to create)", err)
	}
	var goldenReport doctorReport
	if err := json.Unmarshal(golden, &goldenReport); err != nil {
		t.Fatalf("decode golden file: %v", err)
	}
	if report.SchemaVersion != goldenReport.SchemaVersion {
		t.Errorf("schema_version mismatch: got %q, golden %q", report.SchemaVersion, goldenReport.SchemaVersion)
	}
	if len(report.Checks) != len(goldenReport.Checks) {
		t.Fatalf("check count mismatch: got %d, golden %d", len(report.Checks), len(goldenReport.Checks))
	}
	for i := range report.Checks {
		if report.Checks[i].Name != goldenReport.Checks[i].Name {
			t.Errorf("check[%d].Name = %q, golden %q", i, report.Checks[i].Name, goldenReport.Checks[i].Name)
		}
		if report.Checks[i].Code != goldenReport.Checks[i].Code {
			t.Errorf("check[%d].Code = %q, golden %q", i, report.Checks[i].Code, goldenReport.Checks[i].Code)
		}
	}
}

func TestDoctorTextOutputIncludesHints(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/v1/version":
			_, _ = w.Write([]byte(`{"version":"dev"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	t.Setenv("MEM_CONFIG", filepath.Join(t.TempDir(), "missing-config.yaml"))
	t.Setenv("MEM_SERVER", server.URL)
	t.Setenv("MEM_TOKEN", "")
	t.Setenv("MEM_WORKSPACE", "")

	root := newRootCmd()
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor"})
	_ = root.Execute()

	out := stdout.String()
	if !strings.Contains(out, "[FAIL] credentials") {
		t.Errorf("text output missing FAIL credentials:\n%s", out)
	}
	if !strings.Contains(out, "deploy/compose") {
		t.Errorf("text output should mention deploy/compose path in hint:\n%s", out)
	}
	if !strings.Contains(out, "[WARN] workspace") {
		t.Errorf("text output missing WARN workspace:\n%s", out)
	}
}

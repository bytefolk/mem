package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorAllChecksPassJSON(t *testing.T) {
	clearCLIOverrides(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	for _, c := range report.Checks {
		if c.Status == "fail" {
			t.Errorf("check %q failed: %s", c.Name, c.Message)
		}
	}

	checkNames := make(map[string]bool)
	for _, c := range report.Checks {
		checkNames[c.Name] = true
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
	if !strings.Contains(out, "[ok] config") {
		t.Errorf("text output missing config ok:\n%s", out)
	}
	if !strings.Contains(out, "[ok] server") {
		t.Errorf("text output missing server ok:\n%s", out)
	}
	if !strings.Contains(out, "[ok] credentials") {
		t.Errorf("text output missing credentials ok:\n%s", out)
	}
	if !strings.Contains(out, "[ok] workspace") {
		t.Errorf("text output missing workspace ok:\n%s", out)
	}
	if !strings.Contains(out, "all checks passed") {
		t.Errorf("text output missing summary:\n%s", out)
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
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.OK {
		t.Fatal("expected report.OK=false for unreachable server")
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
	if strings.Contains(serverCheck.Message, "127.0.0.1") {
		t.Error("server check message leaks raw URL")
	}
	if strings.Contains(serverCheck.Message, "connection refused") ||
		strings.Contains(serverCheck.Message, "dial tcp") {
		t.Error("server check message leaks raw error value")
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
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"doctor", "--format", "json"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when no credentials")
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
	if !strings.Contains(credCheck.Message, "mem auth login") {
		t.Errorf("credentials check should hint at auth login, got %q", credCheck.Message)
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
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for malformed server URL")
	}

	output := stdout.String()
	if strings.Contains(output, "test-token") {
		t.Error("output leaks token value")
	}
	if strings.Contains(output, "://not-a-url") {
		t.Error("output leaks raw malformed URL")
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
	_ = root.Execute()

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

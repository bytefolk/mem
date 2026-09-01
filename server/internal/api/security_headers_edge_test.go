package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSecurityHeadersEdge is the acceptance test for issue #136.
// It starts nginx as a subprocess with the shipped template, exercises
// proxied, static, and error paths, and asserts single-ownership of
// security headers.
//
// The test is skipped unless MEM_NGINX_BIN points to a real nginx binary
// (matching the pinned image version 1.27.4).
func TestSecurityHeadersEdge(t *testing.T) {
	nginxBin := os.Getenv("MEM_NGINX_BIN")
	if nginxBin == "" {
		t.Skip("MEM_NGINX_BIN not set; skipping edge header test")
	}

	// Verify nginx version.
	out, err := exec.Command(nginxBin, "-v").CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -v: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "nginx/1.27.4") {
		t.Skipf("nginx version mismatch: %s (want 1.27.4)", string(out))
	}

	// Start upstream API server with real securityHeadersMiddleware.
	upstream := httptest.NewServer(securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/ping":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		case "/v1/files":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`[]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})))
	defer upstream.Close()

	// Render nginx config from template.
	tmplPath := filepath.Join("..", "..", "..", "..", "web", "nginx", "default.conf.template")
	tmpl, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}

	conf := string(tmpl)
	conf = strings.ReplaceAll(conf, "${MEMD_UPSTREAM}", upstream.URL)
	conf = strings.ReplaceAll(conf, "${MEM_MAX_BODY_SIZE}", "1k")

	// Write rendered config to temp dir.
	tmpDir := t.TempDir()
	confPath := filepath.Join(tmpDir, "nginx.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Start nginx.
	nginx := exec.Command(nginxBin,
		"-c", confPath,
		"-p", tmpDir,
		"-g", "pid nginx.pid; daemon off;",
	)
	nginx.Stdout = os.Stdout
	nginx.Stderr = os.Stderr
	if err := nginx.Start(); err != nil {
		t.Fatalf("start nginx: %v", err)
	}
	defer nginx.Process.Kill()

	// Wait for nginx to be ready.
	baseURL := "http://127.0.0.1:8080"
	for i := 0; i < 50; i++ {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Run test cases.
	cases := []struct {
		name          string
		method        string
		path          string
		body          io.Reader
		wantStatus    int
		wantHeaders   map[string]string
		wantAbsent    []string
		wantCount     map[string]int
		wantCacheCtrl string // exact Cache-Control value for static assets
	}{
		{
			name:       "proxied /v1/ping 200",
			method:     http.MethodGet,
			path:       "/v1/ping",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "no-referrer",
				"X-Frame-Options":         "DENY",
				"Content-Security-Policy": "default-src 'none'",
				"X-Xss-Protection":        "0",
			},
			wantCount: map[string]int{
				"X-Content-Type-Options": 1,
				"Referrer-Policy":        1,
				"X-Frame-Options":      1,
			},
		},
		{
			name:       "proxied /v1/files 200",
			method:     http.MethodGet,
			path:       "/v1/files",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "no-referrer",
				"X-Frame-Options":         "DENY",
				"Content-Security-Policy": "default-src 'none'",
				"X-Xss-Protection":        "0",
			},
			wantCount: map[string]int{
				"X-Content-Type-Options": 1,
				"Referrer-Policy":        1,
				"X-Frame-Options":      1,
			},
		},
		{
			name:       "nginx local /healthz 200",
			method:     http.MethodGet,
			path:       "/healthz",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"X-Frame-Options":      "DENY",
			},
			wantAbsent: []string{
				"Content-Security-Policy",
				"X-Xss-Protection",
			},
			wantCount: map[string]int{
				"X-Content-Type-Options": 1,
				"Referrer-Policy":        1,
				"X-Frame-Options":      1,
			},
		},
		{
			name:       "nginx local / 200 (spa fallback)",
			method:     http.MethodGet,
			path:       "/",
			wantStatus: http.StatusOK,
			wantHeaders: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"X-Frame-Options":      "DENY",
			},
			wantAbsent: []string{
				"Content-Security-Policy",
				"X-Xss-Protection",
			},
			wantCount: map[string]int{
				"X-Content-Type-Options": 1,
				"Referrer-Policy":        1,
				"X-Frame-Options":      1,
			},
		},
		{
			name:       "nginx 413 on /v1/ (body too large)",
			method:     http.MethodPost,
			path:       "/v1/ping",
			body:       strings.NewReader(strings.Repeat("x", 2048)),
			wantStatus: http.StatusRequestEntityTooLarge,
			wantHeaders: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"X-Frame-Options":      "DENY",
			},
			wantAbsent: []string{
				"Content-Security-Policy",
				"X-Xss-Protection",
			},
			wantCount: map[string]int{
				"X-Content-Type-Options": 1,
				"Referrer-Policy":        1,
				"X-Frame-Options":      1,
			},
		},
		{
			name:       "nginx static 404",
			method:     http.MethodGet,
			path:       "/assets/missing.js",
			wantStatus: http.StatusNotFound,
			wantHeaders: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Referrer-Policy":        "no-referrer",
				"X-Frame-Options":      "DENY",
			},
			wantAbsent: []string{
				"Cache-Control",
			},
			wantCount: map[string]int{
				"X-Content-Type-Options": 1,
				"Referrer-Policy":        1,
				"X-Frame-Options":      1,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != nil {
				body = tc.body
			}
			req, err := http.NewRequest(tc.method, baseURL+tc.path, body)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("do request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tc.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}

			// Check expected headers present with correct values.
			for header, want := range tc.wantHeaders {
				if got := resp.Header.Get(header); got != want {
					t.Errorf("header %q = %q, want %q", header, got, want)
				}
			}

			// Check absent headers.
			for _, header := range tc.wantAbsent {
				if resp.Header.Get(header) != "" {
					t.Errorf("header %q should be absent, got %q", header, resp.Header.Get(header))
				}
			}

			// Check occurrence counts.
			for header, wantCount := range tc.wantCount {
				gotCount := len(resp.Header.Values(header))
				if gotCount != wantCount {
					t.Errorf("header %q occurrence count = %d, want %d; values = %v",
						header, gotCount, wantCount, resp.Header.Values(header))
				}
			}

			// Check Cache-Control for static assets.
			if tc.wantCacheCtrl != "" {
				if got := resp.Header.Get("Cache-Control"); got != tc.wantCacheCtrl {
					t.Errorf("Cache-Control = %q, want %q", got, tc.wantCacheCtrl)
				}
			}
		})
	}
}

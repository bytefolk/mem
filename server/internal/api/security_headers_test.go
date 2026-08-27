package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersMiddleware(t *testing.T) {
	h := securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	for header, want := range map[string]string{
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
		"Content-Security-Policy": "default-src 'none'",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("header %q = %q, want %q", header, got, want)
		}
	}
}

func TestDispositionShouldDownload(t *testing.T) {
	for _, mime := range []string{"text/html", "image/svg+xml", "application/pdf", "text/xml", "application/javascript"} {
		if !dispositionShouldDownload(mime) {
			t.Errorf("dispositionShouldDownload(%q) = false, want true", mime)
		}
	}
	for _, mime := range []string{"image/png", "text/plain", "application/octet-stream", "video/mp4"} {
		if dispositionShouldDownload(mime) {
			t.Errorf("dispositionShouldDownload(%q) = true, want false", mime)
		}
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"a;b\"c\r\nD: b":       "a_b_c_D: b",
		"../../etc/passwd":     ".._.._etc_passwd",
		"../evil.sh":           ".._evil.sh",
		"normal.pdf":           "normal.pdf",
		"":                     "download",
		"\x01\x02":             "download",
		"report.final.v2.docx": "report.final.v2.docx",
		"a: b;c=x - (1).pdf":   "a: b_c=x - (1).pdf",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

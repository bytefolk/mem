package api

import (
	"mime"
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

// TestDispositionShouldDownloadDeclaredMIMEForms guards the shape the data
// actually arrives in: the stored MIME is whatever the client declared, and the
// CLI's own default already carries a charset parameter. An exact match on the
// bare type name therefore misses ordinary HTML/JS/XML uploads.
func TestDispositionShouldDownloadDeclaredMIMEForms(t *testing.T) {
	// The upload path this policy has to survive: `mem put page.html` declares
	// exactly what mime.TypeByExtension returns.
	defaultHTML := mime.TypeByExtension(".html")
	if defaultHTML != "text/html; charset=utf-8" {
		t.Fatalf("fixture assumption drifted: TypeByExtension(.html) = %q", defaultHTML)
	}
	if !dispositionShouldDownload(defaultHTML) {
		t.Errorf("the default `mem put page.html` MIME %q must download", defaultHTML)
	}

	active := []string{
		// Parameters, casing and padding must not downgrade an active type.
		"text/html; charset=utf-8",
		"TEXT/HTML",
		"  text/html  ",
		"image/svg+xml;charset=utf-8",
		"text/xml; charset=UTF-8",
		// A malformed parameter must not buy inline rendering either.
		"text/html; charset",
		"text/html;",
		// Legacy and alternative spellings browsers still honour.
		"application/xhtml+xml",
		"application/x-javascript",
		"text/ecmascript",
		"application/ecmascript",
		// XML vocabularies reach the same parser-activated surface.
		"application/atom+xml",
		"application/rss+xml",
	}
	for _, declared := range active {
		if !dispositionShouldDownload(declared) {
			t.Errorf("dispositionShouldDownload(%q) = false, want true", declared)
		}
	}

	inline := []string{
		"image/png",
		"text/plain; charset=utf-8",
		"text/markdown; charset=utf-8", // previewed as text, not browser-active
		"application/octet-stream",
		"application/zip",
		"",
	}
	for _, declared := range inline {
		if dispositionShouldDownload(declared) {
			t.Errorf("dispositionShouldDownload(%q) = true, want false", declared)
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

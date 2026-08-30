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

// TestContentResponseHeaders pins the two response headers a browser actually
// acts on for every spelling a client can store. Upload keeps the declared MIME
// verbatim, so "Text/HTML;charset=utf-8" and "text/html" are the same type to
// the browser and must be policed identically.
func TestContentResponseHeaders(t *testing.T) {
	cases := []struct {
		storedMIME string
		wantType   string
		wantDisp   string
	}{
		// Interpretable/active types: forced download, however declared.
		{"text/html", "text/html", "attachment"},
		{"TEXT/HTML", "text/html", "attachment"},
		{"  text/html  ", "text/html", "attachment"},
		{"text/html; charset=utf-8", "text/html; charset=utf-8", "attachment"},
		{"Text/Html;charset=UTF-8", "text/html; charset=utf-8", "attachment"},
		{"image/SVG+XML", "image/svg+xml", "attachment"},
		{"application/pdf", "application/pdf", "attachment"},
		{"text/xml", "text/xml", "attachment"},
		{"application/javascript", "application/javascript", "attachment"},
		{"font/woff2", "font/woff2", "attachment"},
		// A declared type the server cannot bound is treated as hostile: no
		// inline preview, and the junk never reaches the browser verbatim.
		{"text/html\r\nX-Injected: 1", "application/octet-stream", "attachment"},
		{"html", "application/octet-stream", "attachment"},
		{"", "application/octet-stream", "attachment"},
		// Inert types stay inline for preview.
		{"image/png", "image/png", "inline"},
		{"image/png;", "image/png", "inline"},
		{"text/plain; charset=utf-8", "text/plain; charset=utf-8", "inline"},
		{"TEXT/PLAIN; CHARSET=GBK", "text/plain; charset=gbk", "inline"},
		// ParseMediaType unquotes parameters, so a non-token charset is
		// dropped rather than echoed back into the response.
		{"text/plain; charset=\"utf 8\"", "text/plain", "inline"},
		{"text/plain; charset=\"x\r\nY: 1\"", "text/plain", "inline"},
		{"text/markdown", "text/markdown", "inline"},
		{"application/octet-stream", "application/octet-stream", "inline"},
		{"video/mp4", "video/mp4", "inline"},
	}
	for _, tc := range cases {
		contentType, disposition := contentResponseHeaders(tc.storedMIME, "note.txt")
		if contentType != tc.wantType {
			t.Errorf("contentResponseHeaders(%q) content type = %q, want %q",
				tc.storedMIME, contentType, tc.wantType)
		}
		want := tc.wantDisp + `; filename="note.txt"`
		if disposition != want {
			t.Errorf("contentResponseHeaders(%q) disposition = %q, want %q",
				tc.storedMIME, disposition, want)
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
		// Fail closed on a declaration the server cannot bound. files.mime is
		// client-supplied, so an unusable value must not take the permissive
		// branch; ingest never stores an empty type anyway (it falls back to
		// the filename extension, then to application/octet-stream), so this
		// costs no real preview.
		"",
		"html",
		"text/html\r\nX-Injected: 1",
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
		// Bidi controls are written as UTF-8 bytes so nothing invisible lives
		// in this file: \xe2\x80\xae is U+202E RIGHT-TO-LEFT OVERRIDE, which
		// makes "报告<RLO>gpj.exe" display to the user as "报告exe.jpg".
		"报告\xe2\x80\xaegpj.exe":   "报告_gpj.exe",
		"\xe2\x80\xaeinvoice.pdf": "invoice.pdf",
		"notes\xe2\x80\xaa":       "notes",
		// The set is Unicode Bidi_Control, not a U+20xx range: ZWJ survives so
		// emoji names stay intact, while \xd8\x9c (U+061C ARABIC LETTER MARK)
		// goes even though it sits outside the bidi block.
		"👨\xe2\x80\x8d👩.png": "👨\xe2\x80\x8d👩.png",
		"ع\xd8\x9ca.pdf":     "ع_a.pdf",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

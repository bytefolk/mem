package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// writeJSON serializes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeError emits {"error": code, "hint": hint} per SPEC §8.2 Agent-friendly
// convention.
func writeError(w http.ResponseWriter, status int, code, hint string) {
	writeJSON(w, status, map[string]any{
		"error": code,
		"hint":  hint,
	})
}

func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// copyTo is a thin wrapper around io.Copy for readability at the call site.
func copyTo(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }

// securityHeadersMiddleware sets defense-in-depth response headers on every
// HTTP response. The API surface is JSON-only; these headers harden against
// MIME-sniffing, clickjacking, referrer leakage, and unintended in-place
// execution of served content (e.g. uploaded files).
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'none'")
		h.Set("X-XSS-Protection", "0")
		next.ServeHTTP(w, r)
	})
}

// dispositionShouldDownload reports whether a stored MIME type must be served
// as a forced download (attachment) rather than inline. Interpretable/active
// types can execute in the browser or trigger parser-based attacks if a
// document is opened in-page; forcing download neuters that surface.
func dispositionShouldDownload(mime string) bool {
	switch mime {
	case "text/html", "text/xhtml", "application/xhtml+xml",
		"image/svg+xml", "text/xml", "application/xml",
		"text/javascript", "application/javascript",
		"application/json", "application/pdf",
		"font/ttf", "font/otf", "font/woff", "font/woff2":
		return true
	}
	return false
}

// sanitizeFilename produces a safe, single-line, path-free filename for use in
// a Content-Disposition header value. It strips CR/LF and other control
// characters (header-injection), quote/semicolon delimiters, and path
// traversal components, collapsing runs of stripped characters to a single
// underscore.
func sanitizeFilename(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 1)
	underscore := false
	for _, r := range name {
		// RFC 7230 §3.2.6: header field values must not contain C0 controls
		// or DEL; CR/LF would enable response splitting.
		if r == 0x7f || r < 0x20 ||
			r == '/' || r == '\\' || r == '"' || r == ';' {
			underscore = true
			continue
		}
		if underscore {
			underscore = false
			if b.Len() > 0 {
				b.WriteByte('_')
			}
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), ` `)
	if out == "" {
		return "download"
	}
	return out
}

package api

import (
	"encoding/json"
	"io"
	"mime"
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
//
// The decision is made on the normalized media type, never the raw declared
// string: the stored MIME is whatever the client declared, and the CLI's own
// default for text types carries a charset parameter
// ("text/html; charset=utf-8").
func dispositionShouldDownload(declaredMIME string) bool {
	switch mediaType := normalizedMediaType(declaredMIME); {
	case mediaType == "text/html", mediaType == "text/xhtml",
		mediaType == "application/xhtml+xml",
		mediaType == "image/svg+xml",
		mediaType == "text/xml", mediaType == "application/xml",
		strings.HasSuffix(mediaType, "+xml"),
		mediaType == "text/javascript", mediaType == "application/javascript",
		mediaType == "application/ecmascript", mediaType == "text/ecmascript",
		mediaType == "application/x-javascript", mediaType == "text/js",
		mediaType == "application/json", mediaType == "application/pdf",
		mediaType == "font/ttf", mediaType == "font/otf",
		mediaType == "font/woff", mediaType == "font/woff2":
		return true
	}
	return false
}

// normalizedMediaType lowercases and strips parameters from a declared media
// type. A value that cannot be parsed falls back to its leading token rather
// than to "unknown", because a browser given "text/html; charset" still treats
// the document as HTML; returning the raw subtype keeps that shape inside the
// active-type policy.
func normalizedMediaType(declaredMIME string) string {
	if mediaType, _, err := mime.ParseMediaType(declaredMIME); err == nil {
		return strings.ToLower(mediaType)
	}
	raw := strings.ToLower(strings.TrimSpace(declaredMIME))
	if i := strings.IndexByte(raw, ';'); i >= 0 {
		raw = strings.TrimSpace(raw[:i])
	}
	return raw
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

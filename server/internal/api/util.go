package api

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode"
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

// activeMediaTypes are the normalized media types a browser can act on —
// execute, render through a document parser, or hand to a font/graphics
// subsystem — if it receives them inline. Every spelling a deployed engine
// still honours has to appear, because the comparison is against the canonical
// type/subtype and a missing alias silently falls through to inline.
//
// The JavaScript entries are RFC 9239's processing equivalents plus the
// pre-standard spellings engines retained; text/javascript1.0 through 1.5 are
// separately registered types, not parameters, so each one is listed.
var activeMediaTypes = map[string]bool{
	"text/html":                true,
	"text/xhtml":               true,
	"application/xhtml+xml":    true,
	"image/svg+xml":            true,
	"text/xml":                 true,
	"application/xml":          true,
	"application/json":         true,
	"application/pdf":          true,
	"text/javascript":          true,
	"application/javascript":   true,
	"application/ecmascript":   true,
	"text/ecmascript":          true,
	"text/js":                  true,
	"text/jscript":             true,
	"text/livescript":          true,
	"text/javascript1.0":       true,
	"text/javascript1.1":       true,
	"text/javascript1.2":       true,
	"text/javascript1.3":       true,
	"text/javascript1.4":       true,
	"text/javascript1.5":       true,
	"application/x-javascript": true,
	"application/x-ecmascript": true,
	"text/x-javascript":        true,
	"text/x-ecmascript":        true,
	// Legacy server spellings for the font tree, kept alongside the prefixes
	// below because vnd.ms-fontobject sits outside every font family.
	"application/vnd.ms-fontobject": true,
}

// activeMediaTypePrefixes cover whole registered families. The IANA font tree
// (font/woff, font/woff2, font/ttf, font/otf, font/collection, font/sfnt, …)
// and the two legacy prefixes servers still emit for it.
var activeMediaTypePrefixes = []string{
	"font/",
	"application/font-",
	"application/x-font-",
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
	mediaType, _, ok := normalizedMediaType(declaredMIME)
	if !ok {
		// The declaration is unusable and attacker-supplied, so assume the
		// worst instead of defaulting to the permissive branch.
		return true
	}
	if activeMediaTypes[mediaType] {
		return true
	}
	// Any "+xml" structured syntax reaches the same XML parser already blocked
	// for text/xml and application/xml.
	if strings.HasSuffix(mediaType, "+xml") {
		return true
	}
	for _, prefix := range activeMediaTypePrefixes {
		if strings.HasPrefix(mediaType, prefix) {
			return true
		}
	}
	return false
}

// normalizedMediaType canonicalises a declared media type the way a browser
// reads one: lowercased "type/subtype" with the parameters split off.
//
// ParseMediaType reports an error for a malformed parameter but still returns
// the type it parsed, which is the part the policy depends on, so the error is
// deliberately not the signal here. ok is false only when no usable type came
// back at all — an empty value, a value with no subtype separator, or one
// padded with CR/LF, which ParseMediaType rejects outright.
func normalizedMediaType(declaredMIME string) (mediaType string, params map[string]string, ok bool) {
	mediaType, params, _ = mime.ParseMediaType(declaredMIME)
	if !strings.Contains(mediaType, "/") {
		return "", nil, false
	}
	return mediaType, params, true
}

// contentResponseHeaders returns the Content-Type and Content-Disposition values
// served for a stored file's bytes.
//
// files.mime holds whatever the uploader declared — file.Service.Put keeps the
// value verbatim — so it is attacker input, not ground truth: a declaration the
// server cannot bound is served as an opaque attachment and never echoed back
// into a response header.
//
// A usable declaration is re-emitted with every parameter it parsed, because
// parameters such as a multipart boundary are part of the representation
// contract and dropping them makes the bytes unreadable to the recipient.
// FormatMediaType is what makes preserving them safe: it quotes or RFC 2231
// percent-encodes a value rather than copying attacker bytes into the header,
// and returns the empty string when the pair cannot be expressed at all.
// ParseMediaType hands back no parameters when it reports a malformed
// declaration, so those still fail closed to an opaque download.
func contentResponseHeaders(storedMIME, name string) (contentType, disposition string) {
	mediaType, params, ok := normalizedMediaType(storedMIME)
	if !ok {
		return "application/octet-stream", `attachment; filename="` + sanitizeFilename(name) + `"`
	}
	if charset, exists := params["charset"]; exists {
		if isHTTPToken(charset) {
			// FormatMediaType lowercases parameter names but not values, and a
			// charset is conventionally served lowercased.
			params["charset"] = strings.ToLower(charset)
		} else {
			delete(params, "charset")
		}
	}
	contentType = mime.FormatMediaType(mediaType, params)
	if contentType == "" {
		return "application/octet-stream", `attachment; filename="` + sanitizeFilename(name) + `"`
	}
	disposition = "inline"
	if dispositionShouldDownload(mediaType) {
		disposition = "attachment"
	}
	return contentType, disposition + `; filename="` + sanitizeFilename(name) + `"`
}

// isHTTPToken reports whether s is a non-empty RFC 7230 token. ParseMediaType
// unquotes parameter values, so charset reaches here as raw client bytes.
// FormatMediaType would happily re-quote a value like "utf 8" and the client
// would still be served a charset it cannot act on, so charset is restricted to
// a token instead; every other parameter is left to FormatMediaType's quoting.
func isHTTPToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' {
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			return false
		}
	}
	return true
}

// bidiControl is the same Unicode property table pathx.ValidateName rejects on
// upload, so the two layers agree on what a name may contain. Stripping it here
// is defense in depth for any name that reaches a response without that
// validation: "报告" + U+202E + "gpj.exe" renders as "报告exe.jpg" in a save
// dialog. U+200D ZERO WIDTH JOINER is not Bidi_Control, so emoji sequences
// survive.
var bidiControl = unicode.Properties["Bidi_Control"]

// sanitizeFilename produces a safe, single-line, path-free filename for use in
// a Content-Disposition header value. It strips CR/LF and other control
// characters (header-injection), quote/semicolon delimiters, bidi controls
// (extension spoofing), and path traversal components, collapsing runs of
// stripped characters to a single underscore.
func sanitizeFilename(name string) string {
	var b strings.Builder
	b.Grow(len(name) + 1)
	underscore := false
	for _, r := range name {
		// The same character predicate pathx.ValidateName rejects on upload, so
		// the two layers cannot disagree about what a name may contain.
		// unicode.IsControl covers C0, DEL and the C1 range that the previous
		// ASCII-only test let through; U+2028/U+2029 are line separators a
		// consumer may still break on; bidi controls spoof the extension.
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
			r == '/' || r == '\\' || r == '"' || r == ';' ||
			(bidiControl != nil && unicode.Is(bidiControl, r)) {
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

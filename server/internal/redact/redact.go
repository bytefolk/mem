// Package redact gates URLs on their way out of the process.
//
// The gate is fail-closed on purpose. A configured URL can carry a password in
// shapes that url.Parse does not report as userinfo: "admin:pw@host" parses as
// Scheme="admin", Opaque="pw@host", User=nil. So "the parser found no userinfo"
// is not evidence that a value is credential-free, and any implementation that
// gates on u.User == nil echoes the credential unchanged. A value the gate
// cannot positively prove safe is withheld whole.
//
// Scrubbing a message that already contains the URL is not a substitute: Go
// renders url.Error with %q, so a quote inside a password arrives escaped and a
// scanner that pairs quotes mis-pairs and replaces nothing. Callers hand text
// here instead and accept withholding when a piece cannot be verified.
package redact

import (
	"errors"
	"net/url"
	"strings"
)

// Placeholder replaces a value the gate cannot prove credential-free. It is a
// fixed token so an operator can tell withholding apart from a real host.
// Deliberately free of '<', '>' and '&': encoding/json escapes those, so an
// angle-bracketed marker would render differently in text and JSON output.
const Placeholder = "[withheld]"

// UserMarker replaces the userinfo of a URL that is otherwise safe to echo.
// It uses only unreserved characters because url.User("***") would
// percent-encode the asterisks.
const UserMarker = "REDACTED"

// APIURLs are the schemes a client base URL may legitimately use.
var APIURLs = []string{"http", "https"}

// StoreURLs are the schemes memd logs: the database DSN and the queue DSN.
var StoreURLs = []string{"http", "https", "postgres", "postgresql", "redis", "rediss", "redis+unix", "unix"}

// URL returns raw with userinfo replaced by UserMarker, or Placeholder when the
// value cannot be proven credential-free.
func URL(raw string, allowed []string) string {
	safe, ok := rewrite(raw, allowed)
	if !ok {
		return Placeholder
	}
	return safe
}

// Text renders diagnostic text — an error message, typically — so that no
// URL-shaped token inside it can carry userinfo out. A single unverifiable
// token withholds the entire message rather than trimming that token, because a
// delimiter inside a credential splits the text into pieces that no longer look
// like a URL, and the piece without the "@" is exactly the half that leaked.
//
// The gate proves the absence of userinfo, not of credentials: a secret in a
// query string (redis://host:6379/?password=x) parses cleanly with User == nil
// and is echoed. That shape is out of scope here and is reported as a residual.
func Text(msg string, allowed []string) string {
	out := msg
	for _, token := range urlTokens(msg) {
		safe, ok := rewrite(token, allowed)
		if !ok {
			return Placeholder
		}
		if safe != token {
			out = strings.Replace(out, token, safe, 1)
		}
	}
	return out
}

// TransportError renders an error returned by http.Client.Do. The URL travels
// through the gate rather than through Go's own masking, which strips the
// password but leaves the username, and the wrapped cause is kept so the
// message still names what failed.
func TransportError(err error, allowed []string) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return Text(ue.Op, allowed) + " " + URL(ue.URL, allowed) + ": " + Text(ue.Err.Error(), allowed)
	}
	return Text(err.Error(), allowed)
}

// rewrite reports whether raw is a URL we can prove carries no credential, and
// returns the form that is safe to echo.
func rewrite(raw string, allowed []string) (string, bool) {
	if raw == "" {
		return "", true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	// Credentials hide in Opaque precisely when the scheme is really userinfo,
	// and an empty or unknown scheme means we are not looking at a transport URL
	// we can reason about.
	if parsed.Opaque != "" || !allowedScheme(parsed.Scheme, allowed) {
		return "", false
	}
	if parsed.User != nil {
		parsed.User = url.User(UserMarker)
	}
	// What leaves the process is the re-serialised form, so verify that instead
	// of trusting the first parse: String() can rebuild something different from
	// the input, and an "@" surviving into the host means userinfo was never in
	// the field we stripped.
	rendered := parsed.String()
	back, err := url.Parse(rendered)
	if err != nil || back.Opaque != "" || back.Host != parsed.Host ||
		!strings.EqualFold(back.Scheme, parsed.Scheme) || strings.Contains(back.Host, "@") {
		return "", false
	}
	if back.User != nil {
		if _, hasPassword := back.User.Password(); hasPassword {
			return "", false
		}
		if back.User.Username() != UserMarker {
			return "", false
		}
	}
	return rendered, true
}

func allowedScheme(scheme string, allowed []string) bool {
	for _, want := range allowed {
		if strings.EqualFold(scheme, want) {
			return true
		}
	}
	return false
}

// urlTokens returns the whitespace- and quote-delimited runs of msg that look
// like they could carry a host or userinfo. Splitting on delimiters is safe
// because any run produced this way still holds either the "@" or the "://"
// that marked the original as credential-shaped, unless the original held
// neither and was never a URL at all.
func urlTokens(msg string) []string {
	var tokens []string
	for _, token := range strings.FieldsFunc(msg, isDelimiter) {
		if strings.Contains(token, "@") || strings.Contains(token, "://") {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func isDelimiter(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '(', ')', '[', ']', '{', '}', '<', '>', ',':
		return true
	}
	return false
}

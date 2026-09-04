package redact

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// secret marks every fixture below. No case may let it reach the returned
// string, and the tests fail closed on the marker rather than on a specific
// redaction shape.
const secret = "s3ntinel-p4ssw0rd"

func TestURLWithholdsShapesTheParserCannotAttribute(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		allowed []string
		want    string
	}{
		{
			name:    "userinfo on a recognised scheme",
			raw:     "http://admin:" + secret + "@mem.internal:8787",
			allowed: APIURLs,
			want:    "http://REDACTED@mem.internal:8787",
		},
		{
			name:    "username only",
			raw:     "http://admin@mem.internal:8787",
			allowed: APIURLs,
			want:    "http://REDACTED@mem.internal:8787",
		},
		{
			name:    "no credential at all is echoed unchanged",
			raw:     "http://localhost:8787",
			allowed: APIURLs,
			want:    "http://localhost:8787",
		},
		{
			name:    "empty value has nothing to leak",
			raw:     "",
			allowed: APIURLs,
			want:    "",
		},
		{
			name:    "store scheme is allowed for a DSN egress",
			raw:     "postgres://mem:" + secret + "@localhost:5432/mem?sslmode=disable",
			allowed: StoreURLs,
			want:    "postgres://REDACTED@localhost:5432/mem?sslmode=disable",
		},
		// The shape this package exists for: url.Parse succeeds, User is nil and
		// the whole credential sits in Opaque, so a u.User != nil gate misses it.
		{
			name:    "no scheme, credential in Opaque",
			raw:     "admin:" + secret + "@mem.internal:8787",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "scheme the egress does not use",
			raw:     "gopher://" + secret + "@mem.internal:8787",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "store scheme on an API egress",
			raw:     "postgres://mem:" + secret + "@localhost:5432/mem",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "parse failure with a space in the host",
			raw:     "http://admin:" + secret + "@ho st.example.com:8787",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "parse failure with a bad percent escape",
			raw:     "http://admin:" + secret + "@mem.internal:%zz",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "parse failure with a space in the password",
			raw:     "http://admin:" + secret + " x@mem.internal:8787",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "parse failure ending in an escape sign",
			raw:     "http://admin:" + secret + "@%",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			name:    "out-of-range port still parses, so userinfo is stripped",
			raw:     "http://admin:" + secret + "@mem.internal:99999999",
			allowed: APIURLs,
			want:    "http://REDACTED@mem.internal:99999999",
		},
		{
			name:    "non-numeric port does not parse",
			raw:     "http://admin:" + secret + "@mem.internal:notaport",
			allowed: APIURLs,
			want:    Placeholder,
		},
		{
			// No scheme is not a recognised transport scheme, so the adjudicated
			// rule withholds it even though the credential did land in User.
			name:    "scheme-relative value has no scheme to check",
			raw:     "//admin:" + secret + "@mem.internal:8787",
			allowed: APIURLs,
			want:    Placeholder,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := URL(tc.raw, tc.allowed)
			if got != tc.want {
				t.Errorf("URL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			if strings.Contains(got, secret) {
				t.Errorf("URL(%q) = %q, leaks the sentinel", tc.raw, got)
			}
		})
	}
}

func TestTextWithholdsWhenAnyCredentialShapedTokenIsUnverifiable(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "plain transport cause survives",
			msg:  `dial tcp: lookup mem.internal: no such host`,
			want: `dial tcp: lookup mem.internal: no such host`,
		},
		{
			name: "well-formed credential URL is redacted in place",
			msg:  `Get "http://admin:` + secret + `@mem.internal:8787/healthz": dial tcp refused`,
			want: `Get "http://REDACTED@mem.internal:8787/healthz": dial tcp refused`,
		},
		{
			name: "no-scheme shape withholds the whole line",
			msg:  `Get "admin:` + secret + `@mem.internal:8787": unsupported protocol scheme "admin"`,
			want: Placeholder,
		},
		{
			name: "quote-escaped password withholds the whole line",
			msg:  `Get "http://admin:` + secret + `\"@mem.internal:8787": context deadline exceeded`,
			want: Placeholder,
		},
		{
			name: "space-split password withholds the whole line",
			msg:  `Get "http://admin:` + secret + ` x@mem.internal:8787": dial tcp`,
			want: Placeholder,
		},
		{
			name: "at sign in a path is not a credential",
			msg:  `GET https://mem.internal/v1/files/report%40mem.internal: permission denied`,
			want: `GET https://mem.internal/v1/files/report%40mem.internal: permission denied`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Text(tc.msg, APIURLs)
			if got != tc.want {
				t.Errorf("Text(%q) = %q, want %q", tc.msg, got, tc.want)
			}
			if strings.Contains(got, secret) {
				t.Errorf("Text(%q) = %q, leaks the sentinel", tc.msg, got)
			}
		})
	}
}

// TestTextGatesAStoreDsnEmbeddedInAForeignError covers the shape that reaches
// memd's fatal log: asynq puts the whole DSN into its parse error and
// queue.NewClient wraps that verbatim.
func TestTextGatesAStoreDsnEmbeddedInAForeignError(t *testing.T) {
	msg := `queue: parse redis url: asynq: could not parse redis uri: ` +
		`parse "redis://:` + secret + `@ho st:6379/0": invalid character " " in host name`

	got := Text(msg, StoreURLs)
	if strings.Contains(got, secret) {
		t.Errorf("Text(%q) = %q, leaks the sentinel", msg, got)
	}
}

// TestTextKnownGapQueryParameterCredentialsAreEchoed characterizes a gap, it does
// not endorse it. The adjudicated rule is about userinfo, and a secret in a query
// parameter parses as an otherwise clean URL, so it is echoed. Closing it belongs
// in a separate decision; this test exists so whoever makes that decision sees a
// named expectation fail rather than rediscovering the behaviour by accident.
func TestTextKnownGapQueryParameterCredentialsAreEchoed(t *testing.T) {
	cases := []string{
		"redis://queue.internal:6379/0?password=" + secret,
		"postgres://mem@db.internal:5432/mem?sslmode=require&password=" + secret,
	}

	for _, msg := range cases {
		got := Text(msg, StoreURLs)
		if !strings.Contains(got, secret) {
			t.Errorf("query-string credential is no longer echoed; update this test to"+
				" assert withholding instead.\n in: %q\nout: %q", msg, got)
		}
	}
}

func TestTransportErrorNamesTheFailureWithoutEchoingCredentials(t *testing.T) {
	sentinel := "http://admin:" + secret + "@mem.internal:8787"
	cases := []struct {
		name string
		err  error
	}{
		{
			name: "url error carrying parsed userinfo",
			err:  &url.Error{Op: "Get", URL: sentinel, Err: errors.New("dial tcp: connection refused")},
		},
		{
			name: "url error whose URL is not a transport scheme",
			err:  &url.Error{Op: "Get", Err: errors.New("unsupported protocol scheme \"admin\""), URL: "admin:" + secret + "@mem.internal:8787"},
		},
		{
			name: "bare error mentioning the URL in prose",
			err:  fmt.Errorf("proxy returned 407 for %s", sentinel),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TransportError(tc.err, APIURLs)
			if strings.Contains(got, secret) {
				t.Errorf("TransportError(%v) = %q, leaks the sentinel", tc.err, got)
			}
			if got == "" {
				t.Errorf("TransportError(%v) = %q, want the failure to stay diagnosable", tc.err, got)
			}
		})
	}

	if got := TransportError(nil, APIURLs); got != "" {
		t.Errorf("TransportError(nil) = %q, want empty", got)
	}
}

// TestTransportErrorKeepsErrorIdentity pins that rendering does not replace the
// chain a caller classifies on: doctor maps a timeout to exit 5 via errors.Is on
// context.DeadlineExceeded, and that has to keep working after the text changes.
func TestTransportErrorKeepsErrorIdentity(t *testing.T) {
	sentinel := "http://admin:" + secret + "@mem.internal:8787"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: sentinel, Err: context.DeadlineExceeded}
	})}
	_, err := client.Get(sentinel)
	if err == nil {
		t.Fatal("expected a transport failure")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("transport error lost its cause: %v", err)
	}
	if rendered := TransportError(err, APIURLs); strings.Contains(rendered, secret) {
		t.Errorf("TransportError = %q, leaks the sentinel", rendered)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

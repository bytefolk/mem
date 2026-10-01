package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGitHubAuthorizationPKCEAndMinimalScopes(t *testing.T) {
	g := &GitHubOAuth{ClientID: "client", RedirectURL: "https://mem.example/v1/auth/github/callback"}
	u, _ := url.Parse(g.AuthorizationURL("state", "verifier"))
	q := u.Query()
	digest := sha256.Sum256([]byte("verifier"))
	if u.Host != "github.com" || q.Get("scope") != "read:user user:email" || q.Get("state") != "state" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) {
		t.Fatalf("incorrect OAuth request: %s", u)
	}
	if strings.Contains(q.Get("scope"), "repo") {
		t.Fatal("sign-in must not request repository access")
	}
}

func TestGitHubExchangeVerifiedPrimaryAndSecretSafeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, emails string
		status       int
		want         bool
	}{
		{"verified", `[{"email":"Primary@Example.test","primary":true,"verified":true}]`, 200, true},
		{"unverified", `[{"email":"primary@example.test","primary":true,"verified":false}]`, 200, false},
		{"non-primary", `[{"email":"secondary@example.test","primary":false,"verified":true}]`, 200, false},
		{"outage", `provider secret should never escape`, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.Form.Get("code_verifier") != "verifier" || r.Form.Get("redirect_uri") != "https://mem.example/callback" || r.Form.Get("client_secret") != "client-secret" {
						t.Error("missing PKCE or exact redirect")
					}
					fmt.Fprint(w, `{"access_token":"provider-secret","token_type":"bearer"}`)
				case "/user":
					if r.Header.Get("Authorization") != "Bearer provider-secret" {
						t.Error("missing provider authorization")
					}
					fmt.Fprint(w, `{"id":47820304,"login":"PeterGuy326"}`)
				case "/user/emails":
					w.WriteHeader(tc.status)
					fmt.Fprint(w, tc.emails)
				default:
					t.Error("unexpected request")
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			g := &GitHubOAuth{ClientID: "client", ClientSecret: "client-secret", RedirectURL: "https://mem.example/callback", HTTPClient: server.Client(), tokenURL: server.URL + "/token", apiURL: server.URL}
			identity, err := g.Exchange(context.Background(), "code", "verifier")
			if (err == nil) != tc.want {
				t.Fatalf("identity=%v error=%v", identity, err)
			}
			if err != nil && (strings.Contains(err.Error(), "provider-secret") || strings.Contains(err.Error(), "client-secret")) {
				t.Fatal("provider secret leaked")
			}
			if tc.want && (identity.Subject != "47820304" || identity.Email != "primary@example.test") {
				t.Fatalf("unexpected identity: %#v", identity)
			}
		})
	}
}

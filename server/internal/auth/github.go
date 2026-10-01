package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrGitHubUnavailable = errors.New("github authentication unavailable")

// GitHubIdentity uses the immutable numeric subject; login/email are metadata.
type GitHubIdentity struct {
	Subject string `json:"subject"`
	Login   string `json:"login"`
	Email   string `json:"email"`
}

// GitHubOAuth deliberately requests no repository or organization access.
// Provider tokens exist only during this exchange and are never persisted.
type GitHubOAuth struct {
	ClientID, ClientSecret, RedirectURL string
	HTTPClient                          *http.Client
	tokenURL, apiURL                    string // test seams; production endpoints are fixed
}

func (g *GitHubOAuth) AuthorizationURL(state, verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"client_id": {g.ClientID}, "redirect_uri": {g.RedirectURL},
		"scope": {"read:user user:email"}, "state": {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(digest[:])},
		"code_challenge_method": {"S256"}, "allow_signup": {"false"},
		"prompt": {"select_account"},
	}
	return "https://github.com/login/oauth/authorize?" + query.Encode()
}

func (g *GitHubOAuth) Exchange(ctx context.Context, code, verifier string) (*GitHubIdentity, error) {
	if code == "" || verifier == "" {
		return nil, ErrGitHubUnavailable
	}
	client := g.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	tokenURL := g.tokenURL
	if tokenURL == "" {
		tokenURL = "https://github.com/login/oauth/access_token"
	}
	apiURL := g.apiURL
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	form := url.Values{"client_id": {g.ClientID}, "client_secret": {g.ClientSecret},
		"redirect_uri": {g.RedirectURL}, "code": {code}, "code_verifier": {verifier}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, ErrGitHubUnavailable
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
	}
	if err := githubJSON(client, req, &token); err != nil || token.AccessToken == "" || token.Error != "" || !strings.EqualFold(token.TokenType, "bearer") {
		return nil, ErrGitHubUnavailable
	}
	get := func(path string, output any) error {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+path, nil)
		if err != nil {
			return ErrGitHubUnavailable
		}
		r.Header.Set("Authorization", "Bearer "+token.AccessToken)
		r.Header.Set("Accept", "application/vnd.github+json")
		r.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		return githubJSON(client, r, output)
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := get("/user", &user); err != nil || user.ID <= 0 || user.Login == "" {
		return nil, ErrGitHubUnavailable
	}
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := get("/user/emails", &emails); err != nil {
		return nil, ErrGitHubUnavailable
	}
	for _, email := range emails {
		if email.Primary && email.Verified && strings.Contains(email.Email, "@") {
			return &GitHubIdentity{Subject: strconv.FormatInt(user.ID, 10), Login: user.Login, Email: strings.ToLower(strings.TrimSpace(email.Email))}, nil
		}
	}
	return nil, ErrGitHubUnavailable
}

func githubJSON(client *http.Client, req *http.Request, output any) error {
	resp, err := client.Do(req)
	if err != nil {
		return ErrGitHubUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrGitHubUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 65537))
	if err := decoder.Decode(output); err != nil {
		return ErrGitHubUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ErrGitHubUnavailable
	}
	return nil
}

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/auth"
	memdb "github.com/PeterGuy326/mem/server/internal/db"
	"github.com/PeterGuy326/mem/server/internal/memory"
	"github.com/PeterGuy326/mem/server/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

type fakeGitHub struct{ onExchange func() }

func (fakeGitHub) AuthorizationURL(state, verifier string) string {
	return "https://github.com/login/oauth/authorize?state=" + state
}
func (provider fakeGitHub) Exchange(_ context.Context, code, verifier string) (*auth.GitHubIdentity, error) {
	if provider.onExchange != nil {
		provider.onExchange()
	}
	identity := &auth.GitHubIdentity{Subject: "47820304", Login: "owner", Email: "owner@example.test"}
	switch code {
	case "link":
		identity.Subject = "222"
		identity.Login = "linked"
		identity.Email = "linked@example.test"
	case "collision":
		identity.Subject = "444"
		identity.Email = "local@example.test"
	case "outsider":
		identity.Subject = "555"
		identity.Email = "outsider@example.test"
	case "second":
		identity.Subject = "333"
		identity.Email = "second@example.test"
	}
	return identity, nil
}

func TestGitHubHTTPIntegration(t *testing.T) {
	dsn := os.Getenv("MEM_OAUTH_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_OAUTH_TEST_DB requires a fresh owned _test database")
	}
	parsed, err := pgxpool.ParseConfig(dsn)
	if err != nil || !strings.HasSuffix(parsed.ConnConfig.Database, "_test") {
		t.Fatal("refusing non-test database")
	}
	ctx := context.Background()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err = database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var users int
	if err = database.Pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil || users != 0 {
		t.Fatal("OAuth acceptance requires a fresh, empty, owned database")
	}
	s := &Server{Auth: auth.New(database.Pool), Memory: memory.New(database.Pool), Workspace: workspace.New(database.Pool), PublicURL: "https://mem.example", GitHub: fakeGitHub{}, GitHubAllowedUserIDs: []string{"47820304", "222", "333", "444"}, GitHubBootstrap: true, RegistrationMode: "disabled", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	router := s.Router()
	request := func(method, path, body, bearer, csrf string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Origin", s.PublicURL)
		r.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if csrf != "" {
			r.Header.Set("X-Mem-CSRF", csrf)
		}
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	start := func(bearer, password string) (string, []*http.Cookie, string) {
		body := `{"intent":"login"}`
		if bearer != "" {
			value, _ := json.Marshal(map[string]string{"intent": "link", "password": password})
			body = string(value)
		}
		w := request("POST", "/v1/auth/github/start", body, bearer, "", nil)
		if w.Code != 200 {
			t.Fatalf("start %d: %s", w.Code, w.Body)
		}
		var response struct {
			URL  string `json:"url"`
			CSRF string `json:"csrf_token"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(response.URL)
		return u.Query().Get("state"), w.Result().Cookies(), response.CSRF
	}
	callback := func(state, code string, cookies []*http.Cookie) *httptest.ResponseRecorder {
		return request("GET", "/v1/auth/github/callback?state="+state+"&code="+code, "", "", "", cookies)
	}
	assertRedirect := func(w *httptest.ResponseRecorder, want string) {
		if w.Code != 303 || w.Header().Get("Location") != want {
			t.Fatalf("callback %d location %s, want %s", w.Code, w.Header().Get("Location"), want)
		}
	}
	visitor := request("POST", "/v1/auth/register", `{"email":"visitor@example.test","password":"visitor-password"}`, "", "", nil)
	if visitor.Code != 403 {
		t.Fatalf("public password enrollment must stay closed: %d", visitor.Code)
	}
	state, cookies, _ := start("", "")
	assertRedirect(callback(state, "outsider", cookies), "/login?github=account_denied")
	state, cookies, _ = start("", "")
	assertRedirect(callback(strings.Repeat("x", 43), "personal", cookies), "/login?github=state_invalid")
	accepted := callback(state, "personal", cookies)
	assertRedirect(accepted, "/login?github=ok")
	assertRedirect(callback(state, "personal", cookies), "/login?github=state_invalid")
	var browser []*http.Cookie
	for _, cookie := range accepted.Result().Cookies() {
		if cookie.Name == s.browserConfig().CookieName {
			browser = append(browser, cookie)
			if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" {
				t.Fatal("unsafe browser cookie")
			}
		}
	}
	if len(browser) != 1 {
		t.Fatal("browser session missing")
	}
	read := request("GET", "/v1/auth/session", "", "", "", browser)
	var session struct {
		Authenticated bool   `json:"authenticated"`
		CSRF          string `json:"csrf_token"`
	}
	if err = json.Unmarshal(read.Body.Bytes(), &session); err != nil || !session.Authenticated {
		t.Fatal("browser readback failed")
	}
	if read.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session response must not be cached")
	}
	if request("GET", "/v1/workspaces", "", "", "", browser).Code != 200 {
		t.Fatal("cookie must pass canonical workspace authorization")
	}
	if request("GET", "/v1/workspaces", "", browser[0].Value, "", nil).Code != 401 {
		t.Fatal("browser cookie cannot be used as an Agent bearer")
	}
	if request("POST", "/v1/memories", `{}`, "", "", browser).Code != 403 {
		t.Fatal("cookie mutation without CSRF must fail")
	}
	mutation := httptest.NewRequest("POST", "/v1/memories", strings.NewReader(`{"kind":"note","content":"source-backed preview","path":"/","source":{"type":"test"}}`))
	mutation.Header.Set("Origin", s.PublicURL)
	mutation.Header.Set("X-Mem-CSRF", session.CSRF)
	mutation.Header.Set("Idempotency-Key", "oauth-preview")
	mutation.AddCookie(browser[0])
	result := httptest.NewRecorder()
	router.ServeHTTP(result, mutation)
	if result.Code != 201 {
		t.Fatalf("cookie memory write %d: %s", result.Code, result.Body)
	}
	logout := request("POST", "/v1/auth/logout", `{}`, "", session.CSRF, browser)
	if logout.Code != 200 || request("GET", "/v1/workspaces", "", "", "", browser).Code != 401 {
		t.Fatal("logout must revoke server-side session")
	}
	state, cookies, _ = start("", "")
	assertRedirect(callback(state, "second", cookies), "/login?github=registration_disabled")
	local, err := s.Auth.CreateUser(ctx, "local@example.test", "current-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Auth.CreateUser(ctx, "other@example.test", "other-password")
	if err != nil {
		t.Fatal(err)
	}
	bearer, _, err := s.Auth.CreateToken(ctx, local.ID, nil, "link-local", auth.AllScopes, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state, cookies, _ = start("", "")
	assertRedirect(callback(state, "collision", cookies), "/login?github=link_required")
	state, cookies, csrf := start(bearer, "current-password")
	if csrf == "" {
		t.Fatal("password reauthentication must establish a bound browser session")
	}
	if request("POST", "/v1/auth/logout", `{}`, "", csrf, cookies).Code != 200 {
		t.Fatal("binding session logout failed")
	}
	assertRedirect(callback(state, "link", cookies), "/login?github=state_invalid")
	state, cookies, csrf = start(bearer, "current-password")
	s.GitHub = fakeGitHub{onExchange: func() {
		if request("POST", "/v1/auth/logout", `{}`, "", csrf, cookies).Code != 200 {
			t.Fatal("exchange-time logout failed")
		}
	}}
	assertRedirect(callback(state, "link", cookies), "/login?github=state_invalid")
	if identity, err := s.Auth.GitHubIdentity(ctx, local.ID); err != nil || identity != nil {
		t.Fatal("exchange-time logout must prevent binding")
	}
	s.GitHub = fakeGitHub{}
	state, cookies, _ = start(bearer, "current-password")
	switchUser := request("POST", "/v1/auth/login", `{"email":"other@example.test","password":"other-password"}`, "", "", cookies)
	if switchUser.Code != 200 {
		t.Fatalf("switch login %d", switchUser.Code)
	}
	assertRedirect(callback(state, "link", cookies), "/login?github=state_invalid")
	state, cookies, _ = start(bearer, "current-password")
	assertRedirect(callback(state, "link", cookies), "/account?github=linked")
	identity, err := s.Auth.GitHubIdentity(ctx, local.ID)
	if err != nil || identity == nil || identity.Subject != "222" {
		t.Fatal("explicit linking failed")
	}
	otherBearer, _, err := s.Auth.CreateToken(ctx, other.ID, nil, "link-other", auth.AllScopes, nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	state, cookies, _ = start(otherBearer, "other-password")
	assertRedirect(callback(state, "link", cookies), "/login?github=identity_conflict")
	state, cookies, _ = start("", "")
	returned := callback(state, "personal", cookies)
	assertRedirect(returned, "/login?github=ok")
	var current *http.Cookie
	for _, cookie := range returned.Result().Cookies() {
		if cookie.Name == s.browserConfig().CookieName {
			current = cookie
		}
	}
	if current == nil {
		t.Fatal("returning owner browser session missing")
	}
	if request("GET", "/v1/workspaces", "", "invalid-old-bearer", "", []*http.Cookie{current}).Code != 401 {
		t.Fatal("invalid Bearer must not fall back to a valid browser cookie")
	}
	var foreignWorkspace uuid.UUID
	if err := database.Pool.QueryRow(ctx, `SELECT id FROM workspaces WHERE resource_owner_user_id=$1`, other.ID).Scan(&foreignWorkspace); err != nil {
		t.Fatal(err)
	}
	foreign := httptest.NewRequest("GET", "/v1/memories", nil)
	foreign.Header.Set("Origin", s.PublicURL)
	foreign.Header.Set("X-Workspace-ID", foreignWorkspace.String())
	foreign.AddCookie(current)
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, foreign)
	if denied.Code != 403 {
		t.Fatalf("browser must not enter another owner's workspace: %d", denied.Code)
	}
	foreign = httptest.NewRequest("GET", "/v1/auth/session", nil)
	foreign.Header.Set("Origin", "https://untrusted.example")
	foreign.AddCookie(current)
	denied = httptest.NewRecorder()
	router.ServeHTTP(denied, foreign)
	if denied.Code != 403 {
		t.Fatal("foreign browser origin must not read session CSRF")
	}
	var secrets int
	if err = database.Pool.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE hash=$1`, auth.HashToken(browser[0].Value)).Scan(&secrets); err != nil || secrets != 0 {
		t.Fatal("browser secret must never become an Agent token")
	}
	// A production downgrade must refuse to orphan the first OAuth-only user.
	sqldb, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()
	if err := goose.DownContext(ctx, sqldb, "migrations"); err == nil || !strings.Contains(err.Error(), "cannot downgrade while GitHub-only users exist") {
		t.Fatalf("unsafe GitHub-only downgrade result: %v", err)
	}
	if identity, err := s.Auth.GitHubIdentity(ctx, local.ID); err != nil || identity == nil {
		t.Fatal("refused downgrade must preserve identities")
	}
}

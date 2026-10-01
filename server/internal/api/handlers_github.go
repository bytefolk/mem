package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/PeterGuy326/mem/server/internal/auth"
	"github.com/google/uuid"
)

type GitHubIdentityProvider interface {
	AuthorizationURL(state, verifier string) string
	Exchange(context.Context, string, string) (*auth.GitHubIdentity, error)
}

func (s *Server) handleAuthCapabilities(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"github": s.GitHub != nil, "password": true, "registration": s.RegistrationMode != "disabled"})
}

func (s *Server) browserConfig() auth.SessionConfig {
	cfg := auth.DefaultSessionConfig()
	cfg.CookieName = "__Host-mem_session"
	if strings.HasPrefix(s.PublicURL, "http://") {
		cfg.Secure = false
		cfg.CookieName = "mem_session"
	}
	if s.SessionTTL > 0 {
		cfg.AbsoluteExpiry = s.SessionTTL
	}
	return cfg
}

func (s *Server) flowCookieName() string {
	if strings.HasPrefix(s.PublicURL, "http://") {
		return "mem_github_flow"
	}
	return "__Host-mem_github_flow"
}

func randomOAuthValue() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func (s *Server) exactBrowserOrigin(r *http.Request) bool {
	return s.PublicURL != "" && r.Header.Get("Origin") == s.PublicURL
}

func (s *Server) handleGitHubStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.GitHub == nil {
		writeError(w, 503, "github_disabled", "GitHub sign-in is not configured")
		return
	}
	if !s.exactBrowserOrigin(r) {
		writeError(w, 403, "origin_denied", "Use the configured application origin")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var body struct {
		Intent   string `json:"intent"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, 400, "invalid_oauth_request", "Invalid sign-in request")
		return
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		writeError(w, 400, "invalid_oauth_request", "Invalid sign-in request")
		return
	}
	if body.Intent == "" {
		body.Intent = "login"
	}
	if body.Intent != "login" && body.Intent != "link" {
		writeError(w, 400, "invalid_oauth_request", "Invalid sign-in intent")
		return
	}
	var userID *uuid.UUID
	var sessionID *uuid.UUID
	var csrf string
	var authGeneration string
	if body.Intent == "link" {
		if header := r.Header.Get("Authorization"); header != "" && !strings.HasPrefix(header, "Bearer ") {
			writeError(w, 401, "reauthentication_required", "Sign in again before linking GitHub")
			return
		}
		// Existing password users must prove the password again. OAuth users
		// must have authenticated in this browser within the last ten minutes.
		if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
			user, token, err := s.Auth.ResolveToken(r.Context(), strings.TrimPrefix(header, "Bearer "))
			if err != nil || !auth.HasScope(token, auth.ScopeAdmin) {
				writeError(w, 401, "reauthentication_required", "Sign in again before linking GitHub")
				return
			}
			verified, err := s.Auth.VerifyPassword(r.Context(), user.Email, body.Password)
			if err != nil || verified.ID != user.ID {
				writeError(w, 401, "reauthentication_required", "Confirm your current password before linking GitHub")
				return
			}
			userID = &user.ID
			cfg := s.browserConfig()
			plain, session, err := s.Auth.CreateSession(r.Context(), user.ID, cfg)
			if err != nil {
				writeError(w, 503, "github_unavailable", "Try again later")
				return
			}
			if prior, _, err := s.resolveBrowser(r); err == nil {
				_ = s.Auth.RevokeSession(r.Context(), prior.ID)
			}
			http.SetCookie(w, auth.SessionCookie(plain, cfg, cfg.AbsoluteExpiry))
			sessionID = &session.ID
			csrf = session.CSRFToken
			authGeneration = session.ID.String()
		} else {
			session, user, err := s.resolveBrowser(r)
			if err != nil || time.Since(session.CreatedAt) > 10*time.Minute || !validBrowserCSRF(r, session) {
				writeError(w, 401, "reauthentication_required", "Sign in again before linking GitHub")
				return
			}
			userID = &user.ID
			sessionID = &session.ID
		}
	}
	state, err := randomOAuthValue()
	if err != nil {
		writeError(w, 503, "github_unavailable", "Try again later")
		return
	}
	verifier, err := randomOAuthValue()
	if err != nil {
		writeError(w, 503, "github_unavailable", "Try again later")
		return
	}
	if err := s.Auth.BeginOAuth(r.Context(), state, body.Intent, userID, sessionID); err != nil {
		writeError(w, 503, "github_unavailable", "Try again later")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.flowCookieName(), Value: state + "." + verifier, Path: "/", MaxAge: 600, HttpOnly: true, Secure: s.browserConfig().Secure, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]string{"url": s.GitHub.AuthorizationURL(state, verifier), "csrf_token": csrf, "session_id": authGeneration})
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.SetCookie(w, &http.Cookie{Name: s.flowCookieName(), Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.browserConfig().Secure, SameSite: http.SameSiteLaxMode})
	fail := func(reason string) { http.Redirect(w, r, "/login?github="+reason, http.StatusSeeOther) }
	if s.GitHub == nil {
		fail("disabled")
		return
	}
	cookie, err := r.Cookie(s.flowCookieName())
	state := r.URL.Query().Get("state")
	if err != nil || len(state) != 43 {
		fail("state_invalid")
		return
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || len(parts[1]) != 43 || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(state)) != 1 {
		fail("state_invalid")
		return
	}
	intent, userID, sessionID, err := s.Auth.ConsumeOAuth(r.Context(), state)
	if err != nil {
		fail("state_invalid")
		return
	}
	if intent == "link" {
		session, actor, err := s.resolveBrowser(r)
		if err != nil || userID == nil || sessionID == nil || actor.ID != *userID || session.ID != *sessionID {
			fail("state_invalid")
			return
		}
	}
	if r.URL.Query().Get("error") != "" {
		fail("denied")
		return
	}
	code := r.URL.Query().Get("code")
	if len(code) == 0 || len(code) > 1024 {
		fail("provider_unavailable")
		return
	}
	identity, err := s.GitHub.Exchange(r.Context(), code, parts[1])
	if err != nil {
		fail("provider_unavailable")
		return
	}
	allowed := false
	for _, id := range s.GitHubAllowedUserIDs {
		if identity.Subject == id {
			allowed = true
			break
		}
	}
	if !allowed {
		fail("account_denied")
		return
	}
	mode := "disabled"
	if s.GitHubBootstrap {
		mode = "first_user"
	}
	user, err := s.Auth.GitHubUser(r.Context(), *identity, userID, sessionID, mode)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthState):
			fail("state_invalid")
		case errors.Is(err, auth.ErrExplicitLinkRequired):
			fail("link_required")
		case errors.Is(err, auth.ErrIdentityConflict):
			fail("identity_conflict")
		case errors.Is(err, auth.ErrRegistrationDisabled):
			fail("registration_disabled")
		default:
			fail("provider_unavailable")
		}
		return
	}
	if intent == "link" {
		if s.Log != nil {
			s.Log.Info("github_identity_linked", "user_id", user.ID, "provider_subject", identity.Subject)
		}
		http.Redirect(w, r, "/account?github=linked", http.StatusSeeOther)
		return
	}
	cfg := s.browserConfig()
	plain, _, err := s.Auth.CreateSession(r.Context(), user.ID, cfg)
	if err != nil {
		fail("provider_unavailable")
		return
	}
	if prior, _, err := s.resolveBrowser(r); err == nil {
		_ = s.Auth.RevokeSession(r.Context(), prior.ID)
	}
	http.SetCookie(w, auth.SessionCookie(plain, cfg, cfg.AbsoluteExpiry))
	if s.Log != nil {
		s.Log.Info("github_identity_authenticated", "intent", intent, "user_id", user.ID, "provider_subject", identity.Subject)
	}
	http.Redirect(w, r, "/login?github=ok", http.StatusSeeOther)
}

func (s *Server) resolveBrowser(r *http.Request) (*auth.Session, *auth.User, error) {
	if s.GitHub == nil {
		return nil, nil, auth.ErrSessionNotFound
	}
	input, err := r.Cookie(s.browserConfig().CookieName)
	if err != nil {
		return nil, nil, auth.ErrSessionNotFound
	}
	session, user, _, err := s.Auth.ResolveSession(r.Context(), input.Value, s.browserConfig())
	return session, user, err
}

func validBrowserCSRF(r *http.Request, session *auth.Session) bool {
	return session != nil && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Mem-CSRF")), []byte(session.CSRFToken)) == 1
}

// authenticateBrowser returns false only when no browser cookie was supplied,
// allowing the existing machine Bearer authentication contract to run.
func (s *Server) authenticateBrowser(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
	if _, err := r.Cookie(s.browserConfig().CookieName); err != nil {
		return false
	}
	session, user, err := s.resolveBrowser(r)
	if err != nil {
		writeError(w, 401, "session_expired", "Sign in again")
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.PublicURL {
		writeError(w, 403, "origin_denied", "Use the configured application origin")
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
		if !s.exactBrowserOrigin(r) || !validBrowserCSRF(r, session) {
			writeError(w, 403, "csrf_denied", "Reload the application and try again")
			return true
		}
	}
	// This projection is internal authorization metadata, never a minted API
	// token. Existing workspace membership/path middleware still applies.
	projection := &auth.Token{ID: session.ID, UserID: user.ID, Name: "browser-session", Scopes: auth.AllScopes, Paths: []string{}, ExpiresAt: &session.ExpiresAt}
	ctx := context.WithValue(r.Context(), ctxActor, user)
	ctx = context.WithValue(ctx, ctxUser, user)
	ctx = context.WithValue(ctx, ctxToken, projection)
	next.ServeHTTP(w, r.WithContext(ctx))
	return true
}

func (s *Server) handleBrowserSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.PublicURL {
		writeError(w, 403, "origin_denied", "Use the configured application origin")
		return
	}
	session, user, err := s.resolveBrowser(r)
	if err != nil {
		writeJSON(w, 200, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": true, "session_id": session.ID, "user": map[string]any{"id": user.ID, "email": user.Email}, "csrf_token": session.CSRFToken, "expires_at": session.ExpiresAt})
}

func (s *Server) handleBrowserLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	session, _, err := s.resolveBrowser(r)
	if err != nil {
		writeError(w, 401, "session_expired", "Sign in again")
		return
	}
	if !s.exactBrowserOrigin(r) || !validBrowserCSRF(r, session) {
		writeError(w, 403, "csrf_denied", "Reload the application and try again")
		return
	}
	if err := s.Auth.RevokeSession(r.Context(), session.ID); err != nil {
		writeError(w, 503, "logout_unavailable", "Try again later")
		return
	}
	http.SetCookie(w, auth.ClearSessionCookie(s.browserConfig()))
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleGitHubIdentity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	user := r.Context().Value(ctxActor).(*auth.User)
	identity, err := s.Auth.GitHubIdentity(r.Context(), user.ID)
	if err != nil {
		writeError(w, 503, "identity_unavailable", "Try again later")
		return
	}
	writeJSON(w, 200, map[string]any{"github": identity})
}

func (s *Server) clearPriorBrowser(w http.ResponseWriter, r *http.Request) {
	if s.GitHub == nil {
		return
	}
	if session, _, err := s.resolveBrowser(r); err == nil {
		_ = s.Auth.RevokeSession(r.Context(), session.ID)
	}
	http.SetCookie(w, auth.ClearSessionCookie(s.browserConfig()))
}

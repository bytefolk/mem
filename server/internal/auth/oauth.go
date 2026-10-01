package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrOAuthState           = errors.New("invalid or expired oauth state")
	ErrIdentityConflict     = errors.New("identity already belongs to another account")
	ErrExplicitLinkRequired = errors.New("sign in to your existing account and explicitly link github")
)

func (s *Service) BeginOAuth(ctx context.Context, state, intent string, userID, sessionID *uuid.UUID) error {
	// Bound global state and discard expired challenges. Secrets stay in an
	// HttpOnly browser cookie; only the one-use state digest is stored here.
	_, err := s.pool.Exec(ctx, `DELETE FROM oauth_challenges WHERE expires_at < now()`)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO oauth_challenges(state_hash, intent, user_id, session_id, expires_at)
	 SELECT $1, $2, $3, $4, $5 WHERE (SELECT count(*) FROM oauth_challenges) < 10000`, HashToken(state), intent, userID, sessionID, time.Now().UTC().Add(10*time.Minute))
	if err == nil && tag.RowsAffected() != 1 {
		return ErrOAuthState
	}
	return err
}

func (s *Service) ConsumeOAuth(ctx context.Context, state string) (string, *uuid.UUID, *uuid.UUID, error) {
	var intent string
	var userID *uuid.UUID
	var sessionID *uuid.UUID
	err := s.pool.QueryRow(ctx, `DELETE FROM oauth_challenges WHERE state_hash=$1 AND expires_at > now() RETURNING intent, user_id, session_id`, HashToken(state)).Scan(&intent, &userID, &sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil, ErrOAuthState
	}
	return intent, userID, sessionID, err
}

// GitHubUser serializes enrollment/linking with local first-user bootstrap.
// Email never authorizes a link, and an existing provider subject never moves.
func (s *Service) GitHubUser(ctx context.Context, identity GitHubIdentity, linkUserID, linkSessionID *uuid.UUID, mode string) (*User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(72466301)`); err != nil {
		return nil, err
	}
	if linkUserID != nil {
		if linkSessionID == nil {
			return nil, ErrOAuthState
		}
		var proof uuid.UUID
		err = tx.QueryRow(ctx, `SELECT id FROM sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at > now() AND last_active > now()-interval '2 hours' FOR UPDATE`, *linkSessionID, *linkUserID).Scan(&proof)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOAuthState
		}
		if err != nil {
			return nil, err
		}
	}
	var user User
	err = tx.QueryRow(ctx, `SELECT u.id, u.email, u.created_at FROM external_identities e JOIN users u ON u.id=e.user_id WHERE e.provider='github' AND e.subject=$1`, identity.Subject).Scan(&user.ID, &user.Email, &user.CreatedAt)
	if err == nil {
		if linkUserID != nil && user.ID != *linkUserID {
			return nil, ErrIdentityConflict
		}
		_, err = tx.Exec(ctx, `UPDATE external_identities SET provider_login=$2, provider_email=$3, updated_at=now() WHERE provider='github' AND subject=$1`, identity.Subject, identity.Login, identity.Email)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if linkUserID != nil {
		err = tx.QueryRow(ctx, `SELECT id,email,created_at FROM users WHERE id=$1`, *linkUserID).Scan(&user.ID, &user.Email, &user.CreatedAt)
		if err != nil {
			return nil, err
		}
		var linked bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM external_identities WHERE provider='github' AND user_id=$1)`, user.ID).Scan(&linked); err != nil {
			return nil, err
		}
		if linked {
			return nil, ErrIdentityConflict
		}
	} else {
		if mode != "first_user" && mode != "open" {
			return nil, ErrRegistrationDisabled
		}
		var existingEmail, anyUser bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email=$1), EXISTS(SELECT 1 FROM users)`, identity.Email).Scan(&existingEmail, &anyUser); err != nil {
			return nil, err
		}
		if existingEmail {
			return nil, ErrExplicitLinkRequired
		}
		if mode == "first_user" && anyUser {
			return nil, ErrRegistrationDisabled
		}
		user = User{ID: uuid.New(), Email: identity.Email, CreatedAt: time.Now().UTC()}
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,email,password_hash,created_at) VALUES($1,$2,'!github-only',$3)`, user.ID, user.Email, user.CreatedAt); err != nil {
			return nil, err
		}
		workspaceID := uuid.New()
		if _, err = tx.Exec(ctx, `INSERT INTO workspaces(id,name,resource_owner_user_id,created_at) VALUES($1,$2,$3,$4)`, workspaceID, identity.Login+"'s workspace", user.ID, user.CreatedAt); err != nil {
			return nil, err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO workspace_memberships(workspace_id,user_id,role,created_at) VALUES($1,$2,'owner',$3)`, workspaceID, user.ID, user.CreatedAt); err != nil {
			return nil, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO external_identities(provider,subject,user_id,provider_login,provider_email) VALUES('github',$1,$2,$3,$4)`, identity.Subject, user.ID, identity.Login, identity.Email); err != nil {
		return nil, fmt.Errorf("link identity: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Service) GitHubIdentity(ctx context.Context, userID uuid.UUID) (*GitHubIdentity, error) {
	var identity GitHubIdentity
	err := s.pool.QueryRow(ctx, `SELECT subject,provider_login,provider_email FROM external_identities WHERE provider='github' AND user_id=$1`, userID).Scan(&identity.Subject, &identity.Login, &identity.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &identity, err
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/PeterGuy326/mem/server/internal/auth"
	"github.com/PeterGuy326/mem/server/internal/vaultsync"
)

const maxVaultCommitBodyBytes = 4 << 20

type VaultSyncService interface {
	Snapshot(context.Context, uuid.UUID, uuid.UUID) (vaultsync.Snapshot, error)
	List(context.Context, uuid.UUID) ([]vaultsync.Summary, error)
	Commit(context.Context, vaultsync.CommitCommand) (vaultsync.Snapshot, error)
}

func (s *Server) handleVaultSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.VaultSync == nil {
		writeVaultError(w, vaultsync.ErrUnavailable)
		return
	}
	values := r.URL.Query()["vaultId"]
	if len(values) != 1 {
		writeVaultError(w, vaultsync.ErrInvalid)
		return
	}
	id, err := vaultsync.ParseVaultID(values[0])
	if err != nil {
		writeVaultError(w, err)
		return
	}
	if !requireTokenPath(w, r, vaultsync.ScopePath(id)) {
		return
	}
	snapshot, err := s.VaultSync.Snapshot(r.Context(), currentWorkspace(r).ID, id)
	if err != nil {
		writeVaultError(w, err)
		return
	}
	writeVaultJSON(w, snapshot)
}

func (s *Server) handleVaultList(w http.ResponseWriter, r *http.Request) {
	if s.VaultSync == nil {
		writeVaultError(w, vaultsync.ErrUnavailable)
		return
	}
	vaults, err := s.VaultSync.List(r.Context(), currentWorkspace(r).ID)
	if err != nil {
		writeVaultError(w, err)
		return
	}
	visible := []vaultsync.Summary{}
	for _, vault := range vaults {
		// Listing is also path-scoped; it must not reveal unauthorized Vault IDs.
		if tokenAllowsPath(r, vaultsync.ScopePath(vault.VaultID)) {
			visible = append(visible, vault)
		}
	}
	writeVaultJSON(w, map[string]any{"vaults": visible})
}

type vaultCommitBody struct {
	VaultID      string               `json:"vaultId"`
	BaseRevision *int64               `json:"baseRevision"`
	Title        *string              `json:"title,omitempty"`
	Entries      []vaultsync.Mutation `json:"entries"`
}

func (s *Server) handleVaultCommit(w http.ResponseWriter, r *http.Request) {
	if s.VaultSync == nil {
		writeVaultError(w, vaultsync.ErrUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxVaultCommitBodyBytes)
	var body vaultCommitBody
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeVaultError(w, vaultsync.ErrLimit)
		} else {
			writeVaultError(w, vaultsync.ErrInvalid)
		}
		return
	}
	if ensureJSONEOF(decoder) != nil || body.BaseRevision == nil {
		writeVaultError(w, vaultsync.ErrInvalid)
		return
	}
	id, err := vaultsync.ParseVaultID(body.VaultID)
	if err != nil {
		writeVaultError(w, err)
		return
	}
	if !requireTokenPath(w, r, vaultsync.ScopePath(id)) {
		return
	}
	actor := r.Context().Value(ctxActor).(*auth.User)
	command, err := vaultsync.NormalizeCommand(vaultsync.CommitCommand{WorkspaceID: currentWorkspace(r).ID,
		ActorUserID: actor.ID, VaultID: id, BaseRevision: *body.BaseRevision, Title: body.Title, Entries: body.Entries})
	if err != nil {
		writeVaultError(w, err)
		return
	}
	snapshot, err := s.VaultSync.Commit(r.Context(), command)
	if err != nil {
		writeVaultError(w, err)
		return
	}
	writeVaultJSON(w, snapshot)
}

func writeVaultJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeVaultError(w http.ResponseWriter, err error) {
	var conflict *vaultsync.RevisionConflict
	switch {
	case errors.As(err, &conflict):
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "vault_revision_conflict", "currentRevision": conflict.CurrentRevision})
	case errors.Is(err, vaultsync.ErrPathConflict):
		writeError(w, http.StatusConflict, "vault_path_conflict", "active Vault paths must be unique; existing notes were preserved")
	case errors.Is(err, vaultsync.ErrLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "vault_limit_exceeded", "Vault payload, note count, or retained history exceeds its bound")
	case errors.Is(err, vaultsync.ErrInvalid):
		writeError(w, http.StatusBadRequest, "vault_request_invalid", "invalid Vault identity, revision, relative path or text payload")
	default:
		writeError(w, http.StatusServiceUnavailable, "vault_unavailable", "Vault sync is unavailable; no successful commit was confirmed")
	}
}

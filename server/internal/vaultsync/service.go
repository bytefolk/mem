// Package vaultsync stores workspace-scoped text Vaults with append-only
// revisions and an atomic Vault-wide compare-and-swap head. File/blob upload
// deduplication is deliberately outside this logical note identity model.
package vaultsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PeterGuy326/mem/server/internal/workspacelock"
)

const (
	SchemaVersion            = "vault-snapshot.v1"
	MaxFileBytes             = 1 << 20
	MaxPropertiesBytes       = 16 << 10
	MaxCurrentBytes          = 16 << 20
	MaxHistoryBytes          = 64 << 20
	MaxEntries               = 1000
	MaxMutationEntries       = 512
	MaxVaults                = 64
	MaxCommits               = 10000
	MaxSafeRevision    int64 = 9007199254740991
)

var (
	ErrInvalid      = errors.New("invalid Vault request")
	ErrLimit        = errors.New("Vault limit exceeded")
	ErrPathConflict = errors.New("Vault path conflict")
	ErrUnavailable  = errors.New("Vault sync is unavailable")
)

type RevisionConflict struct{ CurrentRevision int64 }

func (e *RevisionConflict) Error() string { return "Vault revision conflict" }

type Mutation struct {
	NoteID     uuid.UUID       `json:"noteId"`
	Path       string          `json:"path"`
	Content    string          `json:"content"`
	Deleted    bool            `json:"deleted,omitempty"`
	Properties json.RawMessage `json:"properties,omitempty"`
}

type Entry struct {
	NoteID     uuid.UUID       `json:"noteId"`
	Path       string          `json:"path"`
	Content    string          `json:"content"`
	Revision   int64           `json:"revision"`
	Deleted    bool            `json:"deleted"`
	Properties json.RawMessage `json:"properties"`
}

type Snapshot struct {
	SchemaVersion string     `json:"schemaVersion"`
	VaultID       uuid.UUID  `json:"vaultId"`
	Title         string     `json:"title"`
	Revision      int64      `json:"revision"`
	UpdatedAt     *time.Time `json:"updatedAt"`
	Entries       []Entry    `json:"entries"`
}

type Summary struct {
	VaultID   uuid.UUID `json:"vaultId"`
	Title     string    `json:"title"`
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CommitCommand struct {
	WorkspaceID  uuid.UUID
	ActorUserID  uuid.UUID
	VaultID      uuid.UUID
	BaseRevision int64
	Title        *string
	Entries      []Mutation
}

type Service struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }
func ScopePath(id uuid.UUID) string   { return "/Vaults/" + id.String() }

func ParseVaultID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil || id.String() != raw {
		return uuid.Nil, ErrInvalid
	}
	return id, nil
}

func ValidatePath(value string) error {
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > 512 || path.IsAbs(value) || path.Clean(value) != value ||
		strings.ContainsAny(value, "\\:*?\"<>|") || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ErrInvalid
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return ErrInvalid
		}
	}
	return nil
}

func normalizeProperties(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	} // Existing metadata is preserved when omitted.
	if len(raw) > MaxPropertiesBytes || !utf8.Valid(raw) {
		return nil, ErrLimit
	}
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrInvalid
	}
	if containsZero(value) {
		return nil, ErrInvalid
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, ErrInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, ErrInvalid
	}
	// PostgreSQL jsonb may add spaces between keys/values; this bound applies
	// to its actual persisted representation as well as the compact wire value.
	var indented bytes.Buffer
	if err := json.Indent(&indented, encoded, "", ""); err != nil || indented.Len() > MaxPropertiesBytes {
		return nil, ErrLimit
	}
	return encoded, nil
}

func containsZero(value any) bool {
	switch typed := value.(type) {
	case string:
		return strings.ContainsRune(typed, 0)
	case map[string]any:
		for key, item := range typed {
			if strings.ContainsRune(key, 0) || containsZero(item) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsZero(item) {
				return true
			}
		}
	}
	return false
}

func NormalizeCommand(cmd CommitCommand) (CommitCommand, error) {
	if cmd.WorkspaceID == uuid.Nil || cmd.ActorUserID == uuid.Nil || cmd.VaultID == uuid.Nil || cmd.BaseRevision < 0 ||
		cmd.BaseRevision >= MaxSafeRevision || len(cmd.Entries) > MaxMutationEntries || (len(cmd.Entries) == 0 && cmd.Title == nil) {
		return cmd, ErrInvalid
	}
	if cmd.Title != nil && (!utf8.ValidString(*cmd.Title) || len(*cmd.Title) == 0 || len(*cmd.Title) > 256 ||
		strings.TrimSpace(*cmd.Title) == "" || strings.IndexFunc(*cmd.Title, unicode.IsControl) >= 0) {
		return cmd, ErrInvalid
	}
	seen := make(map[uuid.UUID]bool)
	cmd.Entries = append([]Mutation(nil), cmd.Entries...)
	for i, entry := range cmd.Entries {
		if entry.NoteID == uuid.Nil || seen[entry.NoteID] || ValidatePath(entry.Path) != nil || !utf8.ValidString(entry.Content) ||
			strings.IndexFunc(entry.Content, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' }) >= 0 || (entry.Deleted && entry.Content != "") {
			return cmd, ErrInvalid
		}
		if len(entry.Content) > MaxFileBytes {
			return cmd, ErrLimit
		}
		seen[entry.NoteID] = true
		properties, err := normalizeProperties(entry.Properties)
		if err != nil {
			return cmd, err
		}
		cmd.Entries[i].Properties = properties
	}
	return cmd, nil
}

// plan applies a delta without silently discarding notes, tombstones or a
// conflict. Properties are data and can never grant tools or membership.
func plan(current Snapshot, cmd CommitCommand) (Snapshot, error) {
	if current.Revision != cmd.BaseRevision {
		return Snapshot{}, &RevisionConflict{current.Revision}
	}
	entries := make(map[uuid.UUID]Entry, len(current.Entries)+len(cmd.Entries))
	for _, entry := range current.Entries {
		entries[entry.NoteID] = entry
	}
	next := current
	next.Revision++
	if cmd.Title != nil {
		next.Title = *cmd.Title
	}
	if next.Title == "" {
		next.Title = "Vault " + cmd.VaultID.String()[:8]
	}
	for _, mutation := range cmd.Entries {
		properties := mutation.Properties
		if len(properties) == 0 {
			properties = entries[mutation.NoteID].Properties
		}
		if len(properties) == 0 {
			properties = json.RawMessage(`{}`)
		}
		entries[mutation.NoteID] = Entry{NoteID: mutation.NoteID, Path: mutation.Path, Content: mutation.Content,
			Deleted: mutation.Deleted, Properties: append(json.RawMessage(nil), properties...), Revision: next.Revision}
	}
	if len(entries) > MaxEntries {
		return Snapshot{}, ErrLimit
	}
	paths := make(map[string]bool)
	total := 0
	next.Entries = make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if !entry.Deleted {
			key := strings.ToLower(entry.Path)
			if paths[key] {
				return Snapshot{}, ErrPathConflict
			}
			paths[key] = true
		}
		total += len(entry.Content) + len(entry.Properties) + len(entry.Path)
		if total > MaxCurrentBytes {
			return Snapshot{}, ErrLimit
		}
		next.Entries = append(next.Entries, entry)
	}
	sort.Slice(next.Entries, func(i, j int) bool {
		if next.Entries[i].Path == next.Entries[j].Path {
			return next.Entries[i].NoteID.String() < next.Entries[j].NoteID.String()
		}
		return next.Entries[i].Path < next.Entries[j].Path
	})
	return next, nil
}

type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readSnapshot(ctx context.Context, q querier, workspaceID, vaultID uuid.UUID) (Snapshot, error) {
	snapshot := Snapshot{SchemaVersion: SchemaVersion, VaultID: vaultID, Entries: []Entry{}}
	var updated time.Time
	err := q.QueryRow(ctx, `SELECT title, revision, updated_at FROM vaults WHERE workspace_id=$1 AND id=$2`, workspaceID, vaultID).
		Scan(&snapshot.Title, &snapshot.Revision, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.UpdatedAt = &updated
	rows, err := q.Query(ctx, `SELECT r.note_id, r.path, r.content, r.revision, r.deleted, r.properties
		FROM vault_entry_heads h JOIN vault_entry_revisions r USING(workspace_id,vault_id,note_id,revision)
		WHERE h.workspace_id=$1 AND h.vault_id=$2 ORDER BY r.path COLLATE "C", r.note_id LIMIT $3`, workspaceID, vaultID, MaxEntries+1)
	if err != nil {
		return Snapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var entry Entry
		if err := rows.Scan(&entry.NoteID, &entry.Path, &entry.Content, &entry.Revision, &entry.Deleted, &entry.Properties); err != nil {
			return Snapshot{}, err
		}
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	if len(snapshot.Entries) > MaxEntries {
		return Snapshot{}, ErrLimit
	}
	return snapshot, rows.Err()
}

func (s *Service) Snapshot(ctx context.Context, workspaceID, vaultID uuid.UUID) (Snapshot, error) {
	if s == nil || s.pool == nil {
		return Snapshot{}, ErrUnavailable
	}
	if workspaceID == uuid.Nil || vaultID == uuid.Nil {
		return Snapshot{}, ErrInvalid
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := readSnapshot(ctx, tx, workspaceID, vaultID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (s *Service) List(ctx context.Context, workspaceID uuid.UUID) ([]Summary, error) {
	if s == nil || s.pool == nil {
		return nil, ErrUnavailable
	}
	if workspaceID == uuid.Nil {
		return nil, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `SELECT id,title,revision,updated_at FROM vaults WHERE workspace_id=$1 ORDER BY updated_at DESC,id LIMIT $2`, workspaceID, MaxVaults+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Summary{}
	for rows.Next() {
		var item Summary
		if err := rows.Scan(&item.VaultID, &item.Title, &item.Revision, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if len(result) > MaxVaults {
		return nil, ErrLimit
	}
	return result, rows.Err()
}

func (s *Service) Commit(ctx context.Context, raw CommitCommand) (Snapshot, error) {
	if s == nil || s.pool == nil {
		return Snapshot{}, ErrUnavailable
	}
	cmd, err := NormalizeCommand(raw)
	if err != nil {
		return Snapshot{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Take the existing stronger workspace coordination lock first. Ordinary
	// file writers retain KEY SHARE compatibility, while Vault commits and
	// creation quotas serialize across distinct Vault IDs in this workspace.
	if _, err := workspacelock.ForAIProfileCoordination(ctx, tx, cmd.WorkspaceID); err != nil {
		return Snapshot{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM vaults WHERE workspace_id=$1`, cmd.WorkspaceID).Scan(&count); err != nil {
		return Snapshot{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vaults WHERE workspace_id=$1 AND id=$2)`, cmd.WorkspaceID, cmd.VaultID).Scan(&exists); err != nil {
		return Snapshot{}, err
	}
	if !exists && count >= MaxVaults {
		return Snapshot{}, ErrLimit
	}
	defaultTitle := "Vault " + cmd.VaultID.String()[:8]
	if _, err := tx.Exec(ctx, `INSERT INTO vaults(workspace_id,id,title) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, cmd.WorkspaceID, cmd.VaultID, defaultTitle); err != nil {
		return Snapshot{}, err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM vaults WHERE workspace_id=$1 AND id=$2 FOR UPDATE`, cmd.WorkspaceID, cmd.VaultID).Scan(&revision); err != nil {
		return Snapshot{}, err
	}
	if revision != cmd.BaseRevision {
		return Snapshot{}, &RevisionConflict{revision}
	}
	current, err := readSnapshot(ctx, tx, cmd.WorkspaceID, cmd.VaultID)
	if err != nil {
		return Snapshot{}, err
	}
	next, err := plan(current, cmd)
	if err != nil {
		return Snapshot{}, err
	}
	var historyBytes int64
	var commits int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(octet_length(content)+octet_length(properties::text)+octet_length(path)),0)
		FROM vault_entry_revisions WHERE workspace_id=$1 AND vault_id=$2`, cmd.WorkspaceID, cmd.VaultID).Scan(&historyBytes); err != nil {
		return Snapshot{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM vault_commits WHERE workspace_id=$1 AND vault_id=$2`, cmd.WorkspaceID, cmd.VaultID).Scan(&commits); err != nil {
		return Snapshot{}, err
	}
	changed := make(map[uuid.UUID]Entry)
	for _, entry := range next.Entries {
		if entry.Revision == next.Revision {
			changed[entry.NoteID] = entry
			historyBytes += int64(len(entry.Content) + len(entry.Path) + len(entry.Properties))
		}
	}
	if historyBytes > MaxHistoryBytes || commits >= MaxCommits {
		return Snapshot{}, ErrLimit
	}
	updated := time.Now().UTC()
	if _, err := tx.Exec(ctx, `INSERT INTO vault_commits(workspace_id,vault_id,revision,title,actor_user_id,created_at) VALUES($1,$2,$3,$4,$5,$6)`,
		cmd.WorkspaceID, cmd.VaultID, next.Revision, next.Title, cmd.ActorUserID, updated); err != nil {
		return Snapshot{}, err
	}
	ids := make([]uuid.UUID, 0, len(changed))
	for id := range changed {
		ids = append(ids, id)
	}
	// Delete only mutable pointers inside the atomic transaction. This permits
	// swaps/renames without a transient uniqueness conflict; history remains.
	if _, err := tx.Exec(ctx, `DELETE FROM vault_entry_heads WHERE workspace_id=$1 AND vault_id=$2 AND note_id=ANY($3::uuid[])`, cmd.WorkspaceID, cmd.VaultID, ids); err != nil {
		return Snapshot{}, err
	}
	for _, entry := range changed {
		if _, err := tx.Exec(ctx, `INSERT INTO vault_entry_revisions(workspace_id,vault_id,note_id,revision,path,content,deleted,properties)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)`, cmd.WorkspaceID, cmd.VaultID, entry.NoteID, entry.Revision, entry.Path, entry.Content, entry.Deleted, string(entry.Properties)); err != nil {
			return Snapshot{}, classifyWriteError(err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO vault_entry_heads(workspace_id,vault_id,note_id,revision,path,deleted) VALUES($1,$2,$3,$4,$5,$6)`,
			cmd.WorkspaceID, cmd.VaultID, entry.NoteID, entry.Revision, entry.Path, entry.Deleted); err != nil {
			return Snapshot{}, classifyWriteError(err)
		}
	}
	// Enforce quotas against PostgreSQL's actual jsonb representation, including
	// its spacing, rather than estimating persisted bytes from compact JSON.
	var persistedCurrent, persistedHistory int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(octet_length(r.content)+octet_length(r.properties::text)+octet_length(r.path)),0)
		FROM vault_entry_heads h JOIN vault_entry_revisions r USING(workspace_id,vault_id,note_id,revision)
		WHERE h.workspace_id=$1 AND h.vault_id=$2`, cmd.WorkspaceID, cmd.VaultID).Scan(&persistedCurrent); err != nil {
		return Snapshot{}, err
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(octet_length(content)+octet_length(properties::text)+octet_length(path)),0)
		FROM vault_entry_revisions WHERE workspace_id=$1 AND vault_id=$2`, cmd.WorkspaceID, cmd.VaultID).Scan(&persistedHistory); err != nil {
		return Snapshot{}, err
	}
	if persistedCurrent > MaxCurrentBytes || persistedHistory > MaxHistoryBytes {
		return Snapshot{}, ErrLimit
	}
	if _, err := tx.Exec(ctx, `UPDATE vaults SET title=$3,revision=$4,updated_at=$5 WHERE workspace_id=$1 AND id=$2`, cmd.WorkspaceID, cmd.VaultID, next.Title, next.Revision, updated); err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Snapshot{}, err
	}
	next.UpdatedAt = &updated
	return next, nil
}

func classifyWriteError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) && pgError.Code == "23505" {
		return ErrPathConflict
	}
	if errors.As(err, &pgError) && pgError.Code == "23514" {
		return ErrLimit
	}
	return fmt.Errorf("Vault write failed: %w", err)
}

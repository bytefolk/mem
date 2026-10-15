package vaultsync

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validCommand() CommitCommand {
	return CommitCommand{WorkspaceID: uuid.New(), ActorUserID: uuid.New(), VaultID: uuid.New(),
		Entries: []Mutation{{NoteID: uuid.New(), Path: "知识/空白笔记.md", Content: ""}}}
}

func TestLogicalIdentityEmptyNotesAndCAS(t *testing.T) {
	cmd := validCommand()
	cmd.Entries = append(cmd.Entries, Mutation{NoteID: uuid.New(), Path: "知识/另一个空笔记.md", Content: ""})
	empty := Snapshot{SchemaVersion: SchemaVersion, VaultID: cmd.VaultID, Entries: []Entry{}}
	first, err := plan(empty, cmd)
	if err != nil || first.Revision != 1 || len(first.Entries) != 2 || first.Entries[0].NoteID == first.Entries[1].NoteID {
		t.Fatalf("separate empty note identities: %+v, %v", first, err)
	}
	_, err = plan(first, cmd)
	var conflict *RevisionConflict
	if !errors.As(err, &conflict) || conflict.CurrentRevision != 1 {
		t.Fatalf("stale client must conflict: %v", err)
	}
	if len(first.Entries) != 2 || first.Entries[0].Content != "" {
		t.Fatal("conflict modified original snapshot")
	}
}

func TestDeltaPreservesUntouchedNotesMetadataAndTombstones(t *testing.T) {
	cmd := validCommand()
	cmd.Entries[0].Properties = json.RawMessage(`{"positionIds":["writer"],"source":"user"}`)
	first, _ := plan(Snapshot{VaultID: cmd.VaultID}, cmd)
	noteID := cmd.Entries[0].NoteID
	cmd.BaseRevision = 1
	cmd.Entries = []Mutation{{NoteID: noteID, Path: "知识/改名.md", Content: "第二版"},
		{NoteID: uuid.New(), Path: "共享 SOP.md", Content: "流程"}}
	second, err := plan(first, cmd)
	if err != nil || len(second.Entries) != 2 {
		t.Fatal(err)
	}
	for _, entry := range second.Entries {
		if entry.NoteID == noteID && !strings.Contains(string(entry.Properties), "writer") {
			t.Fatal("omitted properties erased metadata")
		}
	}
	cmd.BaseRevision = 2
	cmd.Entries = []Mutation{{NoteID: noteID, Path: "知识/改名.md", Deleted: true}}
	third, err := plan(second, cmd)
	if err != nil || len(third.Entries) != 2 {
		t.Fatal("deletion must retain tombstone and unrelated note", err)
	}
	for _, entry := range third.Entries {
		if entry.NoteID == noteID && (!entry.Deleted || entry.Content != "" || entry.Revision != 3) {
			t.Fatal("invalid tombstone")
		}
	}
	if first.Entries[0].Path != "知识/空白笔记.md" || first.Revision != 1 {
		t.Fatal("later revisions mutated previous snapshot")
	}
}

func TestPathsDuplicatesAndUntrustedPropertiesAreValidated(t *testing.T) {
	for _, name := range []string{"../secret.md", "/absolute.md", "a\\b.md", "a/../b.md", "a//b.md", "a\x00.md", "C:notes.md", "dir/a ", "dir/a.", "a?.md"} {
		if ValidatePath(name) == nil {
			t.Fatalf("unsafe path accepted: %q", name)
		}
	}
	if err := ValidatePath("客户 ACME/项目 決策.md"); err != nil {
		t.Fatal(err)
	}
	for _, properties := range []string{`null`, `[]`, `{"source":"\u0000"}`, `{"source":true} trailing`} {
		cmd := validCommand()
		cmd.Entries[0].Properties = json.RawMessage(properties)
		if _, err := NormalizeCommand(cmd); err == nil {
			t.Fatalf("invalid metadata accepted: %s", properties)
		}
	}
	cmd := validCommand()
	cmd.Entries[0].Properties = json.RawMessage(`{"tools":["write"],"authority":"admin"}`)
	normalized, err := NormalizeCommand(cmd)
	if err != nil || normalized.ActorUserID != cmd.ActorUserID || normalized.WorkspaceID != cmd.WorkspaceID {
		t.Fatal("metadata changed authorization", err)
	}
	cmd.Entries = append(cmd.Entries, cmd.Entries[0])
	if _, err := NormalizeCommand(cmd); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate note ID accepted")
	}
	cmd = validCommand()
	cmd.Entries[0].Content = "secret"
	cmd.Entries[0].Deleted = true
	if _, err := NormalizeCommand(cmd); !errors.Is(err, ErrInvalid) {
		t.Fatal("tombstone retained content")
	}
}

func TestActivePathsConflictCaseInsensitivelyAndSwapsAreValid(t *testing.T) {
	cmd := validCommand()
	cmd.Entries = []Mutation{{NoteID: uuid.New(), Path: "A.md"}, {NoteID: uuid.New(), Path: "a.md"}}
	if _, err := plan(Snapshot{VaultID: cmd.VaultID}, cmd); !errors.Is(err, ErrPathConflict) {
		t.Fatal("Windows path collision accepted")
	}
	cmd.Entries[1].Path = "B.md"
	first, err := plan(Snapshot{VaultID: cmd.VaultID}, cmd)
	if err != nil {
		t.Fatal(err)
	}
	cmd.BaseRevision = 1
	cmd.Entries[0].Path = "B.md"
	cmd.Entries[1].Path = "A.md"
	if _, err := plan(first, cmd); err != nil {
		t.Fatal("atomic path swap rejected", err)
	}
}

func TestBoundsAndTitleOnlyCAS(t *testing.T) {
	cmd := validCommand()
	cmd.Entries[0].Content = strings.Repeat("x", MaxFileBytes+1)
	if _, err := NormalizeCommand(cmd); !errors.Is(err, ErrLimit) {
		t.Fatal("oversize note accepted")
	}
	cmd = validCommand()
	cmd.Entries = nil
	if _, err := NormalizeCommand(cmd); err == nil {
		t.Fatal("empty mutation accepted")
	}
	title := "岗位知识库"
	cmd.Title = &title
	cmd, err := NormalizeCommand(cmd)
	if err != nil {
		t.Fatal(err)
	}
	result, err := plan(Snapshot{VaultID: cmd.VaultID}, cmd)
	if err != nil || result.Title != title || result.Revision != 1 || len(result.Entries) != 0 {
		t.Fatal("title-only commit failed", err)
	}
	large := Snapshot{VaultID: cmd.VaultID}
	for i := 0; i < MaxEntries; i++ {
		large.Entries = append(large.Entries, Entry{NoteID: uuid.New(), Path: "old.md", Deleted: true, Properties: json.RawMessage(`{}`)})
	}
	cmd.Entries = []Mutation{{NoteID: uuid.New(), Path: "new.md"}}
	if _, err := plan(large, cmd); !errors.Is(err, ErrLimit) {
		t.Fatal("tombstones were excluded from note-count bound")
	}
}

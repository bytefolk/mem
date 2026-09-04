package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeterGuy326/mem/server/internal/redact"
	"github.com/PeterGuy326/mem/server/internal/workspacebundle"
)

func TestRedactURLCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "postgres",
			raw:  "postgres://mem:database-secret@postgres:5432/mem?sslmode=disable",
			want: "postgres://REDACTED@postgres:5432/mem?sslmode=disable",
		},
		{
			name: "redis",
			raw:  "redis://:redis-secret@redis:6379/0",
			want: "redis://REDACTED@redis:6379/0",
		},
		{
			// The old scrubber echoed this raw because it only masked values with a
			// password; the gate treats any userinfo as a credential.
			name: "username only",
			raw:  "postgres://mem@postgres:5432/mem?sslmode=disable",
			want: "postgres://REDACTED@postgres:5432/mem?sslmode=disable",
		},
		{
			// url.Parse reads the scheme as "redis" and parks the rest in Opaque,
			// so a u.User check never fires.
			name: "redis without a transport scheme",
			raw:  "redis:redis-secret@redis:6379/0",
			want: redact.Placeholder,
		},
		{
			name: "postgres that fails to parse",
			raw:  "postgres://mem:database-secret@post gres:5432/mem",
			want: redact.Placeholder,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := redactURLCredentials(test.raw)
			if got != test.want {
				t.Fatalf("redactURLCredentials(%q) = %q, want %q", test.raw, got, test.want)
			}
			if strings.Contains(got, "secret") {
				t.Fatalf("credentials leaked from %q: %q", test.raw, got)
			}
		})
	}
}

func TestRedactURLCredentialsLeavesCredentialFreeValuesAlone(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"redis://redis:6379/0",
		"http://mem.internal:8787",
	} {
		if got := redactURLCredentials(raw); got != raw {
			t.Fatalf("redactURLCredentials(%q) = %q", raw, got)
		}
	}
}

// TestRedactURLCredentialsWithholdsWhatItCannotVerify pins the fail-closed half:
// these shapes carry no visible password today, but the gate cannot prove that
// from a parse it either failed or had to attribute to an unknown scheme, so
// logging them at all would be guessing.
func TestRedactURLCredentialsWithholdsWhatItCannotVerify(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"redis:6379",
		"://not-a-url",
	} {
		if got := redactURLCredentials(raw); got != redact.Placeholder {
			t.Fatalf("redactURLCredentials(%q) = %q, want %q", raw, got, redact.Placeholder)
		}
	}
}

func TestWorkspaceTransferBundleLimitsAreConservativeAndConsistent(t *testing.T) {
	defaults := workspacebundle.DefaultLimits()
	limits := workspaceTransferBundleLimits()

	if limits.MaxEntries != 100_000 ||
		limits.MaxTotalSize != 32<<30 ||
		limits.MaxTotalMetadataSize != 128<<20 ||
		limits.MaxRecordsPerIndex != 100_000 {
		t.Fatalf("workspace transfer limits = %+v", limits)
	}
	if limits.MaxEntrySize != defaults.MaxEntrySize ||
		limits.MaxMetadataEntrySize != defaults.MaxMetadataEntrySize ||
		limits.MaxCompressionRatio != defaults.MaxCompressionRatio ||
		limits.MaxJSONDepth != defaults.MaxJSONDepth ||
		limits.MaxPathDepth != defaults.MaxPathDepth ||
		limits.MaxIndexLineBytes != defaults.MaxIndexLineBytes {
		t.Fatalf(
			"non-overridden limits diverged: got=%+v defaults=%+v",
			limits,
			defaults,
		)
	}
}

func TestPrepareWorkspaceTransferTmpDirCreatesPrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "workspace-transfer")

	resolved, err := prepareWorkspaceTransferTmpDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved path is not absolute: %q", resolved)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("temporary directory mode = %v", info.Mode())
	}
}

func TestPrepareWorkspaceTransferTmpDirRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	publicDir := filepath.Join(root, "public")
	if err := os.Mkdir(publicDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(publicDir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{publicDir, file, symlink} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			_, err := prepareWorkspaceTransferTmpDir(path)
			if err == nil {
				t.Fatalf("expected unsafe temp dir %q to be rejected", path)
			}
			if !strings.Contains(err.Error(), "require 0700") &&
				!strings.Contains(err.Error(), "must be a directory") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestPrepareWorkspaceTransferTmpDirEmptyUsesSystemDefault(t *testing.T) {
	resolved, err := prepareWorkspaceTransferTmpDir("  ")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "" {
		t.Fatalf("resolved path = %q, want empty", resolved)
	}
}

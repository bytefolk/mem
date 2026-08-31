package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PeterGuy326/mem/server/internal/auth"
	memdb "github.com/PeterGuy326/mem/server/internal/db"
	"github.com/PeterGuy326/mem/server/internal/file"
	"github.com/PeterGuy326/mem/server/internal/folder"
	"github.com/PeterGuy326/mem/server/internal/workspace"
)

// memoryObjectStore keeps uploaded bytes so the content route can be served
// without provisioning object storage in CI.
type memoryObjectStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (store *memoryObjectStore) Put(
	_ context.Context,
	key string,
	body io.Reader,
	_ int64,
	_ string,
) error {
	pending, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.objects == nil {
		store.objects = map[string][]byte{}
	}
	store.objects[key] = pending
	return nil
}

func (store *memoryObjectStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	object, ok := store.objects[key]
	if !ok {
		return nil, errors.New("object not found")
	}
	return io.NopCloser(bytes.NewReader(object)), nil
}

func (store *memoryObjectStore) Delete(_ context.Context, key string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.objects, key)
	return nil
}

// TestContentDispositionHTTPPostgres drives the route a browser actually hits:
// upload through the API with a declared MIME type, then read the response
// headers back off a real socket. The unit-level policy tests pin the header
// strings; this pins that the declared value survives ingest (files.mime is
// stored verbatim) and that the handler serves what the policy decides.
func TestContentDispositionHTTPPostgres(t *testing.T) {
	dsn := os.Getenv("MEM_TEST_DB")
	if dsn == "" {
		t.Skip("MEM_TEST_DB not set; skipping content disposition HTTP integration test")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse MEM_TEST_DB: %v", err)
	}
	if !strings.HasSuffix(config.ConnConfig.Database, "_test") {
		t.Fatalf(
			"refusing to modify non-test database %q; MEM_TEST_DB must end in _test",
			config.ConnConfig.Database,
		)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	database, err := memdb.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	authService := auth.New(database.Pool)
	workspaceService := workspace.New(database.Pool)
	folderService := folder.New(database.Pool)
	user, err := authService.CreateUser(
		ctx,
		"content-disposition-"+uuid.NewString()+"@example.test",
		"integration-password",
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := database.Pool.Exec(cleanupCtx, `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
			t.Errorf("clean up user: %v", err)
		}
	})
	currentWorkspace, err := workspaceService.Resolve(ctx, user.ID, nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	token, _, err := authService.CreateToken(
		ctx,
		user.ID,
		&currentWorkspace.ID,
		"content-disposition-writer",
		[]string{auth.ScopeRead, auth.ScopeWrite},
		nil,
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	server := httptest.NewServer((&Server{
		Auth:      authService,
		File:      file.New(database.Pool, &memoryObjectStore{}, folderService),
		Folder:    folderService,
		Workspace: workspaceService,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}).Router())
	t.Cleanup(server.Close)

	cases := []struct {
		declaredMIME string
		name         string
		wantType     string
		wantDisp     string
	}{{
		// The shape `mem put page.html` produces, and missed before
		// normalization: a browser renders this as a document.
		declaredMIME: "text/html; charset=utf-8",
		name:         "page.html",
		wantType:     "text/html; charset=utf-8",
		wantDisp:     "attachment",
	}, {
		declaredMIME: "Text/HTML",
		name:         "mixed-case.html",
		wantType:     "text/html",
		wantDisp:     "attachment",
	}, {
		declaredMIME: "application/atom+xml",
		name:         "feed.atom",
		wantType:     "application/atom+xml",
		wantDisp:     "attachment",
	}, {
		declaredMIME: "text/x-javascript",
		name:         "legacy-script.js",
		wantType:     "text/x-javascript",
		wantDisp:     "attachment",
	}, {
		// Boundary is required for a recipient to parse multipart content;
		// normalizing the type must not discard it.
		declaredMIME: "multipart/mixed; boundary=mem-boundary",
		name:         "bundle.mime",
		wantType:     "multipart/mixed; boundary=mem-boundary",
		wantDisp:     "inline",
	}, {
		declaredMIME: "image/png",
		name:         "photo.png",
		wantType:     "image/png",
		wantDisp:     "inline",
	}, {
		// A declaration the server cannot bound: fail closed, and never let the
		// stored string describe itself into a second response header.
		declaredMIME: "text/html\r\nX-Injected: 1",
		name:         "junk-mime.html",
		wantType:     "application/octet-stream",
		wantDisp:     "attachment",
	}}

	for index, tc := range cases {
		// Dedup is keyed on content hash per user, so every case needs distinct
		// bytes or it would read back the first row's stored MIME type.
		body := "<script>alert(" + string(rune('0'+index)) + ")</script>"
		endpoint := server.URL + "/v1/files?stream=1&name=" + url.QueryEscape(tc.name) +
			"&mime=" + url.QueryEscape(tc.declaredMIME) +
			"&size=" + strconv.Itoa(len(body))
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			endpoint,
			strings.NewReader(body),
		)
		if err != nil {
			t.Fatalf("build upload request: %v", err)
		}
		request.Header.Set("Authorization", "Bearer "+token)
		uploadResponse, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("upload %q: %v", tc.name, err)
		}
		uploadBody, _ := io.ReadAll(uploadResponse.Body)
		uploadResponse.Body.Close()
		if uploadResponse.StatusCode != http.StatusCreated {
			t.Fatalf("upload %q status = %d body=%s", tc.name, uploadResponse.StatusCode, uploadBody)
		}
		var uploaded struct {
			File struct {
				ID string `json:"id"`
			} `json:"file"`
		}
		if err := json.Unmarshal(uploadBody, &uploaded); err != nil {
			t.Fatalf("decode upload response %s: %v", uploadBody, err)
		}

		downloadRequest, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			server.URL+"/v1/files/"+uploaded.File.ID+"/content",
			nil,
		)
		if err != nil {
			t.Fatalf("build download request: %v", err)
		}
		downloadRequest.Header.Set("Authorization", "Bearer "+token)
		downloadResponse, err := server.Client().Do(downloadRequest)
		if err != nil {
			t.Fatalf("download %q: %v", tc.name, err)
		}
		downloaded, err := io.ReadAll(downloadResponse.Body)
		downloadResponse.Body.Close()
		if err != nil {
			t.Fatalf("read download body: %v", err)
		}
		if downloadResponse.StatusCode != http.StatusOK {
			t.Fatalf("download %q status = %d", tc.name, downloadResponse.StatusCode)
		}
		if string(downloaded) != body {
			t.Errorf("declared %q: served body = %q, want %q", tc.declaredMIME, downloaded, body)
		}
		if got := downloadResponse.Header.Get("Content-Type"); got != tc.wantType {
			t.Errorf("declared %q: Content-Type = %q, want %q", tc.declaredMIME, got, tc.wantType)
		}
		disposition := downloadResponse.Header.Get("Content-Disposition")
		if !strings.HasPrefix(disposition, tc.wantDisp+`; filename="`+tc.name+`"`) {
			t.Errorf("declared %q: Content-Disposition = %q, want prefix %q",
				tc.declaredMIME, disposition, tc.wantDisp+`; filename="`+tc.name+`"`)
		}
		if got := downloadResponse.Header.Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("declared %q: X-Content-Type-Options = %q, want nosniff", tc.declaredMIME, got)
		}
		if got := downloadResponse.Header.Get("Content-Security-Policy"); got == "" {
			t.Errorf("declared %q: Content-Security-Policy is missing", tc.declaredMIME)
		}
		if got := downloadResponse.Header.Get("X-Injected"); got != "" {
			t.Errorf("declared %q: smuggled response header reached the wire: %q", tc.declaredMIME, got)
		}
	}
}

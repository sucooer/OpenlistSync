package syncer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"openlist-sync/internal/client"
)

// envResp configures the canned responses for a fake OpenList.
// path -> {statusCode, code, message}
type canned struct {
	httpStatus int
	code       int
	message    string
}

type envResp map[string]canned

// newFakeOpenList spins up an httptest server that records every mkdir call
// and returns the matching canned response. Other paths return 404 by default.
func newFakeOpenList(t *testing.T, table envResp) (*httptest.Server, *[]string, func()) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Path string `json:"path"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, body.Path)
		mu.Unlock()
		resp, ok := table[body.Path]
		if !ok {
			resp, ok = table["__default__"]
			if !ok {
				resp = canned{http.StatusNotFound, 404, "no rule"}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.httpStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    resp.code,
			"message": resp.message,
			"data":    nil,
		})
	}))
	cleanup := func() {
		srv.Close()
	}
	return srv, &calls, cleanup
}

// newClientForTest builds a Client wired to srv (using a fake token), so
// dirs.Ensure triggers real HTTP calls (going through doJSON).
func newClientForTest(t *testing.T, srv *httptest.Server) *client.Client {
	t.Helper()
	return client.New(srv.URL, "test-tok", "", "", false, func(string, ...any) {})
}

func TestEnsureAlreadyExistsIsIdempotent(t *testing.T) {
	srv, calls, done := newFakeOpenList(t, envResp{
		"移动":     {http.StatusOK, 500, "file already exists"},
		"移动/foo": {http.StatusOK, 500, "file already exists"},
	})
	defer done()
	c := newClientForTest(t, srv)
	m := newRemoteDirMaker(c, func(string, ...any) {})

	if err := m.Ensure(context.Background(), "/移动/foo"); err != nil {
		t.Fatalf("already exists should be idempotent success, got: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("expected 2 mkdir attempts (parent + target), got %d", len(*calls))
	}

	// Second Ensure on the same path: should not even hit network (cached).
	if err := m.Ensure(context.Background(), "/移动/foo"); err != nil {
		t.Fatalf("ensured path should be cached as created: %v", err)
	}
	if len(*calls) != 2 {
		t.Fatalf("second Ensure should be cached, got %d total calls", len(*calls))
	}
}

func TestEnsureStorageNotFoundIsSoftSkip(t *testing.T) {
	srv, calls, done := newFakeOpenList(t, envResp{
		"__default__": {http.StatusOK, 500, "storage not found rawPath: /移动"},
	})
	defer done()
	c := newClientForTest(t, srv)
	var warnings []string
	var mu sync.Mutex
	m := newRemoteDirMaker(c, func(format string, a ...any) {
		mu.Lock()
		warnings = append(warnings, strings.TrimSpace(format))
		mu.Unlock()
	})

	// Ensure a path several levels deep; OpenList says storage-not-found for each.
	// Expectation: returns nil (we move on), all calls are made, one warning logged.
	err := m.Ensure(context.Background(), "/移动/foo/bar/baz")
	if err != nil {
		t.Fatalf("storage-not-found should NOT be fatal, got: %v", err)
	}
	if len(*calls) != 4 {
		t.Fatalf("expected 4 mkdir attempts (every segment), got %d", len(*calls))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(warnings) != 4 {
		t.Fatalf("expected 4 warnings (one per mkdir result), got %d", len(warnings))
	}
}

func TestEnsurePermissionDeniedIsSoftSkip(t *testing.T) {
	// 阿里 OSS 等场景常见: mkdir 返回 "permission denied" 但对象 PUT 仍可工作。
	srv, _, done := newFakeOpenList(t, envResp{
		"__default__": {http.StatusOK, 403, "permission denied"},
	})
	defer done()
	c := newClientForTest(t, srv)
	m := newRemoteDirMaker(c, func(string, ...any) {})
	if err := m.Ensure(context.Background(), "/oss/bucket/key"); err != nil {
		t.Fatalf("permission-denied should be soft skip, got: %v", err)
	}
}

func TestEnsureUnknownErrorIsFatal(t *testing.T) {
	srv, _, done := newFakeOpenList(t, envResp{
		"__default__": {http.StatusInternalServerError, 500, "internal server error"},
	})
	defer done()
	c := newClientForTest(t, srv)
	m := newRemoteDirMaker(c, func(string, ...any) {})
	err := m.Ensure(context.Background(), "/x/y/z")
	if err == nil {
		t.Fatal("unknown error should still bubble up")
	}
	if !strings.Contains(err.Error(), "internal server error") {
		t.Fatalf("error should preserve original message, got: %v", err)
	}
}

func TestEnsureSuccessPath(t *testing.T) {
	srv, calls, done := newFakeOpenList(t, envResp{
		"__default__": {http.StatusOK, 200, ""},
	})
	defer done()
	c := newClientForTest(t, srv)
	m := newRemoteDirMaker(c, func(string, ...any) {})
	if err := m.Ensure(context.Background(), "/a/b/c"); err != nil {
		t.Fatalf("plain success should yield nil, got: %v", err)
	}
	if len(*calls) != 3 {
		t.Fatalf("expected 3 mkdir attempts, got %d", len(*calls))
	}
	// Second Ensure on same path: cached.
	if err := m.Ensure(context.Background(), "/a/b/c"); err != nil {
		t.Fatalf("second should be cached: %v", err)
	}
	if len(*calls) != 3 {
		t.Fatalf("calls must not grow on cache hit, got %d", len(*calls))
	}
}

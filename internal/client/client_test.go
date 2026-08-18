package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer spins up an httptest server that returns a single canned
// response, with optional auth header assertions. Useful as a stand-in for
// OpenList: just configure the JSON envelope and status, no real protocol.
func newTestServer(t *testing.T, status int, code int, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(envelope{
			Code:    code,
			Message: message,
			Data:    json.RawMessage("null"),
		})
	}))
}

// TestUploadEscapesSpacesAsPercent20 verifies that upload paths use %20 for
// spaces instead of '+', which some OpenList versions treat as a literal plus.
func TestUploadEscapesSpacesAsPercent20(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		gotPath = r.Header.Get("File-Path")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope{Code: 200, Data: json.RawMessage("null")})
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token", "", "", false, func(string, ...any) {})
	if err := c.Upload(context.Background(), "cover.jpg", "/音乐/杨千嬅 Minor Classics Live音乐会 (2011)/cover.jpg", false, strings.NewReader("data"), 4); err != nil {
		t.Fatalf("upload should succeed: %v", err)
	}
	if strings.Contains(gotPath, "+") {
		t.Fatalf("File-Path must not encode spaces as '+': %q", gotPath)
	}
	if !strings.Contains(gotPath, "%20") {
		t.Fatalf("File-Path should percent-encode spaces: %q", gotPath)
	}
}

// TestMkdirStorageNotFoundIsAnnotated verifies that the raw "storage not
// found" error from OpenList is decorated with a Chinese hint pointing the
// operator to the OpenList admin storage list, since the real OpenList
// message "storage not found rawpath:/移动" is otherwise opaque.
func TestMkdirStorageNotFoundIsAnnotated(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, 500, "filed get storage :storage not found rawpath :/移动")
	defer srv.Close()

	c := New(srv.URL, "test-token", "", "", false, func(string, ...any) {})
	err := c.Mkdir(context.Background(), "/移动/sub")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "storage not found") {
		t.Fatalf("original OpenList message should remain, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "OpenList") {
		t.Fatalf("should append operator-facing hint, got: %s", err.Error())
	}
}

// TestOtherErrorsPassThrough ensures we don't accidentally annotate
// unrelated errors with the storage hint.
func TestOtherErrorsPassThrough(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, 401, "unauthorized: invalid token")
	defer srv.Close()

	c := New(srv.URL, "test-token", "", "", false, func(string, ...any) {})
	err := c.Mkdir(context.Background(), "/x")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if strings.Contains(err.Error(), "storage not found") {
		t.Fatalf("non-storage error shouldn't be annotated, got: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "code=401") {
		t.Fatalf("error should still carry the OpenList code, got: %s", err.Error())
	}
}

// TestMkdirSuccessDoesNotAnnotate sanity-checks the path where the server
// returns 200/200 and we expect a nil error.
func TestMkdirSuccessDoesNotAnnotate(t *testing.T) {
	srv := newTestServer(t, http.StatusOK, 200, "")
	defer srv.Close()

	c := New(srv.URL, "test-token", "", "", false, func(string, ...any) {})
	if err := c.Mkdir(context.Background(), "/x"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

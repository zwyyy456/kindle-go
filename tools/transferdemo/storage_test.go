package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStorage(t *testing.T) (*StorageServer, http.Handler, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	server := &StorageServer{
		Dir: t.TempDir(), AllowedOrigin: "https://transfer.example.com",
		Secret: []byte("01234567890123456789012345678901"), MaxDownloads: 3,
		Now: func() time.Time { return now },
	}
	if err := server.prepare(); err != nil {
		t.Fatal(err)
	}
	return server, server.routes(), now
}

func storageToken(t *testing.T, server *StorageServer, method, object string, size int64, expires time.Time, nonce string) string {
	t.Helper()
	token, err := signStorageClaims(storageClaims{
		Version: 1, Method: method, ObjectID: object, ExpectedSize: size,
		ExpiresAt: expires.Unix(), Nonce: nonce, Filename: "sample book.azw3",
	}, server.Secret)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func uploadObject(t *testing.T, server *StorageServer, handler http.Handler, object string, payload []byte, now time.Time) string {
	t.Helper()
	token := storageToken(t, server, http.MethodPut, object, int64(len(payload)), now.Add(time.Hour), strings.Repeat("a", 32))
	request := httptest.NewRequest(http.MethodPut, "/upload/"+token, bytes.NewReader(payload))
	request.Header.Set("Origin", server.AllowedOrigin)
	request.Header.Set("Content-Type", "application/octet-stream")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	return recorder.Header().Get("X-Content-SHA256")
}

func TestStorageUploadIsAtomicHashedAndCannotBeReplayed(t *testing.T) {
	server, handler, now := newTestStorage(t)
	object := strings.Repeat("1", objectIDN*2)
	payload := []byte("kindle transfer")
	gotHash := uploadObject(t, server, handler, object, payload, now)
	if gotHash != hashHex(payload) {
		t.Fatalf("hash = %q", gotHash)
	}
	if _, err := os.Stat(server.partPath(object)); !os.IsNotExist(err) {
		t.Fatalf("part file remains: %v", err)
	}
	meta, err := server.readMetadata(object)
	if err != nil || meta.State != stateReady || meta.ActualSize != int64(len(payload)) {
		t.Fatalf("metadata = %#v, err = %v", meta, err)
	}

	token := storageToken(t, server, http.MethodPut, object, int64(len(payload)), now.Add(time.Hour), strings.Repeat("b", 32))
	request := httptest.NewRequest(http.MethodPut, "/upload/"+token, bytes.NewReader(payload))
	request.Header.Set("Origin", server.AllowedOrigin)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("overwrite status = %d", recorder.Code)
	}
}

func TestStorageRejectsWrongSizeAndAllowsNewAuthorization(t *testing.T) {
	server, handler, now := newTestStorage(t)
	object := strings.Repeat("2", objectIDN*2)
	token := storageToken(t, server, http.MethodPut, object, 20, now.Add(time.Hour), strings.Repeat("a", 32))
	request := httptest.NewRequest(http.MethodPut, "/upload/"+token, strings.NewReader("short"))
	request.Header.Set("Origin", server.AllowedOrigin)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("wrong-size status = %d, body = %q", recorder.Code, recorder.Body.String())
	}

	payload := []byte("retry")
	token = storageToken(t, server, http.MethodPut, object, int64(len(payload)), now.Add(time.Hour), strings.Repeat("b", 32))
	request = httptest.NewRequest(http.MethodPut, "/upload/"+token, bytes.NewReader(payload))
	request.Header.Set("Origin", server.AllowedOrigin)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("retry status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
}

func TestStorageCORSPreflightIsExact(t *testing.T) {
	server, handler, _ := newTestStorage(t)
	request := httptest.NewRequest(http.MethodOptions, "/upload/token", nil)
	request.Header.Set("Origin", server.AllowedOrigin)
	request.Header.Set("Access-Control-Request-Method", "PUT")
	request.Header.Set("Access-Control-Request-Headers", "Content-Type")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent ||
		recorder.Header().Get("Access-Control-Allow-Origin") != server.AllowedOrigin ||
		recorder.Header().Get("Access-Control-Allow-Headers") != "Content-Type" {
		t.Fatalf("preflight = %d %#v", recorder.Code, recorder.Header())
	}

	request = httptest.NewRequest(http.MethodOptions, "/upload/token", nil)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Access-Control-Request-Method", "PUT")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d", recorder.Code)
	}
}

func TestStorageDownloadSupportsHeadRangeRepeatAndBusyPage(t *testing.T) {
	server, handler, now := newTestStorage(t)
	object := strings.Repeat("3", objectIDN*2)
	payload := []byte("0123456789")
	uploadObject(t, server, handler, object, payload, now)
	token := storageToken(t, server, http.MethodGet, object, int64(len(payload)), now.Add(time.Hour), strings.Repeat("c", 32))
	path := "/download/" + token

	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, path, nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || server.downloads.Load() != 0 {
		t.Fatalf("HEAD = %d bytes=%d downloads=%d", head.Code, head.Body.Len(), server.downloads.Load())
	}
	for i := 0; i < 2; i++ {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Range", "bytes=2-5")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusPartialContent || recorder.Body.String() != "2345" {
			t.Fatalf("range #%d = %d %q", i, recorder.Code, recorder.Body.String())
		}
	}

	server.downloads.Store(3)
	busy := httptest.NewRecorder()
	handler.ServeHTTP(busy, httptest.NewRequest(http.MethodGet, path, nil))
	if busy.Code != http.StatusServiceUnavailable || busy.Header().Get("Retry-After") == "" ||
		!strings.Contains(busy.Body.String(), "线路繁忙") {
		t.Fatalf("busy response = %d %#v %q", busy.Code, busy.Header(), busy.Body.String())
	}
	server.downloads.Store(0)
}

func TestStorageDeleteRevokesObject(t *testing.T) {
	server, handler, now := newTestStorage(t)
	object := strings.Repeat("4", objectIDN*2)
	uploadObject(t, server, handler, object, []byte("delete me"), now)
	token := storageToken(t, server, http.MethodDelete, object, 9, now.Add(time.Minute), strings.Repeat("d", 32))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodDelete, "/objects/"+token, nil))
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %q", recorder.Code, recorder.Body.String())
	}
	if _, err := os.Stat(server.objectPath(object)); !os.IsNotExist(err) {
		t.Fatalf("object still exists: %v", err)
	}
}

func TestStorageStartupRemovesOrphanParts(t *testing.T) {
	dir := t.TempDir()
	part := filepath.Join(dir, strings.Repeat("5", objectIDN*2)+".part")
	if err := os.WriteFile(part, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	server := &StorageServer{Dir: dir, Secret: []byte("01234567890123456789012345678901"), MaxDownloads: 3}
	if err := server.prepare(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("orphan part remains: %v", err)
	}
}

func TestStorageCleanupDeletesExpiredReadyObject(t *testing.T) {
	server, handler, now := newTestStorage(t)
	object := strings.Repeat("7", objectIDN*2)
	uploadObject(t, server, handler, object, []byte("expired"), now)
	meta, err := server.readMetadata(object)
	if err != nil {
		t.Fatal(err)
	}
	meta.ExpiresAt = now.Add(-time.Second).Unix()
	if err := server.writeMetadata(meta); err != nil {
		t.Fatal(err)
	}
	if err := server.cleanupExpiredFiles(); err != nil {
		t.Fatal(err)
	}
	meta, err = server.readMetadata(object)
	if err != nil || meta.State != stateExpired {
		t.Fatalf("metadata = %#v, err = %v", meta, err)
	}
	if _, err := os.Stat(server.objectPath(object)); !os.IsNotExist(err) {
		t.Fatalf("expired object remains: %v", err)
	}
}

func TestStorageMetadataIsValidJSON(t *testing.T) {
	server, handler, now := newTestStorage(t)
	object := strings.Repeat("6", objectIDN*2)
	uploadObject(t, server, handler, object, []byte("json"), now)
	payload, err := os.ReadFile(server.metadataPath(object))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	if value["state"] != string(stateReady) {
		t.Fatalf("metadata state = %#v", value["state"])
	}
}

var _ = io.Discard

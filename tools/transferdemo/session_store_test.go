package main

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func newTestSessionStore(t *testing.T, now *time.Time) *sessionStore {
	t.Helper()
	store, err := openSessionStore(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return *now }
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestUploadStartedIsIdempotentAndCannotExtendExpiry(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := newTestSessionStore(t, &now)
	session, token, err := store.create(modeServerA, "book.azw3", "application/octet-stream", 42, hashHex([]byte("book")))
	if err != nil {
		t.Fatal(err)
	}
	if got := session.ExpiresAt; !got.Equal(now.Add(createdLifetime)) {
		t.Fatalf("created expiry = %v", got)
	}
	started, err := store.markUploadStarted(session.Code, token, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	wantExpiry := now.Add(2 * time.Hour)
	if !started.ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("started expiry = %v, want %v", started.ExpiresAt, wantExpiry)
	}
	firstStartedAt := *started.FirstUploadStartedAt

	now = now.Add(45 * time.Minute)
	repeated, err := store.markUploadStarted(session.Code, token, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !repeated.ExpiresAt.Equal(wantExpiry) || !repeated.FirstUploadStartedAt.Equal(firstStartedAt) {
		t.Fatalf("repeat extended transfer: %#v", repeated)
	}
}

func TestCreatedSessionExpiresAfterTenMinutes(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := newTestSessionStore(t, &now)
	session, token, err := store.create(modeR2, "book.epub", "application/epub+zip", 10, hashHex([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(createdLifetime)
	if _, err := store.markUploadStarted(session.Code, token, 2*time.Hour); err == nil {
		t.Fatal("expired CREATED session started")
	}
}

func TestReadyLookupRejectsUploadingAndRevokedSessions(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := newTestSessionStore(t, &now)
	session, token, err := store.create(modeServerA, "book.pdf", "application/pdf", 10, hashHex([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.getReady(session.Code); !errorsIsSQLNoRows(err) {
		t.Fatalf("uploading lookup error = %v", err)
	}
	if _, err := store.markUploadStarted(session.Code, token, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := store.markReady(session.Code, token, "etag"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.getReady(session.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := store.revoke(session.Code, token); err != nil {
		t.Fatal(err)
	}
	if _, err := store.getReady(session.Code); !errorsIsSQLNoRows(err) {
		t.Fatalf("revoked lookup error = %v", err)
	}
}

func TestExpiredOfflineSessionIsQueuedForPhysicalDeletion(t *testing.T) {
	now := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC)
	store := newTestSessionStore(t, &now)
	session, token, err := store.create(modeR2, "book.pdf", "application/pdf", 10, hashHex([]byte("x")))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.markUploadStarted(session.Code, token, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := store.markReady(session.Code, token, "etag"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	pending, err := store.pendingExpiredObjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].StorageObjectID != session.StorageObjectID {
		t.Fatalf("pending = %#v", pending)
	}
	if err := store.markStorageDeleted(pending[0].ID); err != nil {
		t.Fatal(err)
	}
	pending, err = store.pendingExpiredObjects()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after cleanup = %#v, err = %v", pending, err)
	}
}

func errorsIsSQLNoRows(err error) bool { return err == sql.ErrNoRows }

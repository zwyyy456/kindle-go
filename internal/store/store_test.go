package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCreatesAndReopensSchema(t *testing.T) {
	root := t.TempDir()
	first, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := first.db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
}

func TestCreateOriginalPersistsImmutableFile(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	created, err := store.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("正文"), now)
	if err != nil {
		t.Fatal(err)
	}
	if created.Original.SHA256 == "" || created.Original.Size != int64(len("正文")) {
		t.Fatalf("original = %#v", created.Original)
	}
	path, err := store.ResolveRel(created.Original.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "正文" {
		t.Fatalf("stored source = %q", data)
	}
	loaded, ok, err := store.Book(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("book = %#v, %v, %v", loaded, ok, err)
	}
	if loaded.Original.SHA256 != created.Original.SHA256 {
		t.Fatalf("loaded hash = %q, want %q", loaded.Original.SHA256, created.Original.SHA256)
	}
}

func TestMigrateLegacyIndexPreservesFilesAndLatestOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "converted"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "originals", "old.txt"), []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "converted", "old.azw3"), []byte("artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyIndex(t, root, legacyIndex{Records: []legacyRecord{{
		ID: "old-book", OriginalName: "旧书.txt", UploadedAt: now, LastError: "old error",
		Original: legacyFile{Name: "old.txt", RelPath: "originals/old.txt", Format: "txt", Size: 1, CreatedAt: now},
		Output:   legacyFile{Name: "old.azw3", RelPath: "converted/old.azw3", Format: "azw3", Size: 1, CreatedAt: now.Add(time.Minute)},
	}}})
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	book, ok, err := store.Book(context.Background(), "old-book")
	if err != nil || !ok {
		t.Fatalf("book = %#v, %v, %v", book, ok, err)
	}
	if book.Original.Size != int64(len("source")) || book.LatestArtifact.Size != int64(len("artifact")) {
		t.Fatalf("migrated book = %#v", book)
	}
	if book.LegacyLastError != "old error" {
		t.Fatalf("legacy error = %q", book.LegacyLastError)
	}
	if _, err := os.Stat(filepath.Join(root, "index.json")); err != nil {
		t.Fatalf("legacy index was not preserved: %v", err)
	}
}

func TestLegacyMigrationRollsBackAllRecords(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "originals", "valid.txt"), []byte("valid"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyIndex(t, root, legacyIndex{Records: []legacyRecord{
		{ID: "valid", OriginalName: "valid.txt", UploadedAt: now, Original: legacyFile{Name: "valid.txt", RelPath: "originals/valid.txt", Format: "txt", CreatedAt: now}},
		{ID: "unsafe", OriginalName: "unsafe.txt", UploadedAt: now, Original: legacyFile{Name: "unsafe.txt", RelPath: "../unsafe.txt", Format: "txt", CreatedAt: now}},
	}})
	if store, err := Open(root); err == nil {
		store.Close()
		t.Fatal("expected unsafe legacy path to fail migration")
	}
	if err := os.Remove(filepath.Join(root, "index.json")); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	books, err := store.AllBooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(books) != 0 {
		t.Fatalf("migration left %d books after rollback", len(books))
	}
}

func TestLegacyMigrationPreservesPreviouslyDownloadableFormat(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "originals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "originals", "manual.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	writeLegacyIndex(t, root, legacyIndex{Records: []legacyRecord{{
		ID: "manual", OriginalName: "manual.pdf", UploadedAt: now,
		Original: legacyFile{Name: "manual.pdf", RelPath: "originals/manual.pdf", Format: "pdf", CreatedAt: now},
	}}})
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	book, ok, err := store.Book(context.Background(), "manual")
	if err != nil || !ok {
		t.Fatalf("book = %#v, %v, %v", book, ok, err)
	}
	if book.SourceFormat != "pdf" || book.Original.Format != "pdf" {
		t.Fatalf("legacy format was not preserved: %#v", book)
	}
}

func TestOpenReconcilesPendingFiles(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	created, err := store.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE files SET state = 'pending' WHERE id = ?`, created.Original.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	book, ok, err := reopened.Book(context.Background(), created.ID)
	if err != nil || !ok {
		t.Fatalf("book = %#v, %v, %v", book, ok, err)
	}
	if book.Original.State != "ready" {
		t.Fatalf("original state = %q", book.Original.State)
	}
}

func TestResolveRelRejectsEscapes(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, value := range []string{"../outside", "..", "/absolute"} {
		if _, err := store.ResolveRel(value); err == nil {
			t.Fatalf("ResolveRel(%q) unexpectedly succeeded", value)
		}
	}
}

func TestRuntimeLockLifecycleAndStaleTakeover(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)

	if err := store.ClaimRuntimeLock(ctx, "first", 101, now, 15*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.HeartbeatRuntimeLock(ctx, "first", now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	lock, ok, err := store.RuntimeLock(ctx)
	if err != nil || !ok {
		t.Fatalf("runtime lock = %#v, %v, %v", lock, ok, err)
	}
	if lock.InstanceID != "first" || lock.PID != 101 || !lock.HeartbeatAt.Equal(now.Add(5*time.Second)) {
		t.Fatalf("runtime lock = %#v", lock)
	}

	err = store.ClaimRuntimeLock(ctx, "second", 202, now.Add(10*time.Second), 15*time.Second)
	var held *RuntimeLockHeldError
	if !errors.As(err, &held) || held.Lock.InstanceID != "first" || held.Lock.PID != 101 {
		t.Fatalf("fresh lock claim error = %#v", err)
	}
	if err := store.ClaimRuntimeLock(ctx, "second", 202, now.Add(21*time.Second), 15*time.Second); err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	if err := store.HeartbeatRuntimeLock(ctx, "first", now.Add(22*time.Second)); err == nil {
		t.Fatal("previous owner unexpectedly renewed a taken-over lock")
	}
	if err := store.ReleaseRuntimeLock(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.RuntimeLock(ctx); err != nil || ok {
		t.Fatalf("released runtime lock still exists: ok=%v err=%v", ok, err)
	}
}

func TestRuntimeLockValidatesOwner(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := store.ClaimRuntimeLock(ctx, "", 1, time.Now(), time.Second); err == nil {
		t.Fatal("empty instance ID unexpectedly accepted")
	}
	if err := store.ClaimRuntimeLock(ctx, "instance", 0, time.Now(), time.Second); err == nil {
		t.Fatal("invalid PID unexpectedly accepted")
	}
	if err := store.ClaimRuntimeLock(ctx, "instance", 1, time.Now(), 0); err == nil {
		t.Fatal("invalid stale duration unexpectedly accepted")
	}
}

func writeLegacyIndex(t *testing.T, root string, index legacyIndex) {
	t.Helper()
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

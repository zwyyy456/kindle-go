package store

import (
	"context"
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

func TestStartupFailsArtifactCommitInterruptedAfterRename(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	book, err := store.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	task, err := store.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "generate_epub", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if claimed, ok, err := store.ClaimNextTask(context.Background(), []string{"generate_epub"}, time.Now()); err != nil || !ok || claimed.ID != task.ID {
		t.Fatalf("claim = %#v, %v, %v", claimed, ok, err)
	}
	path := filepath.Join(root, "artifacts", book.ID, "pending.epub")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("partial artifact")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	digest, _, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, source_file_id, task_id, created_at) VALUES('pending-artifact', ?, 'artifact', 'pending', 'epub', 'pending.epub', ?, ?, ?, ?, ?, ?)`, book.ID, filepath.ToSlash(filepath.Join("artifacts", book.ID, "pending.epub")), digest, len(data), book.Original.ID, task.ID, formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForWorker(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	recoveryTime := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	if _, err := reopened.RecoverRunningTasks(context.Background(), recoveryTime); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("interrupted artifact remains after restart: %v", err)
	}
	file, ok, err := reopened.File(context.Background(), "pending-artifact")
	if err != nil || ok {
		t.Fatalf("interrupted artifact remains visible = %#v, %v, %v", file, ok, err)
	}
	recoveredTask, ok, err := reopened.Task(context.Background(), task.ID)
	if err != nil || !ok || recoveredTask.Status != "failed" || recoveredTask.ErrorCode != "task_process_interrupted" {
		t.Fatalf("recovered task = %#v, %v, %v", recoveredTask, ok, err)
	}
	events, err := reopened.TaskEvents(context.Background(), task.ID)
	if err != nil || len(events) < 3 || events[len(events)-1].Stage != "failed" || !strings.Contains(events[len(events)-1].Message, "task_process_interrupted") {
		t.Fatalf("recovery events = %#v, %v", events, err)
	}
}

func TestStartupRollsBackArtifactCommitInterruptedBeforeRename(t *testing.T) {
	root := t.TempDir()
	storage, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	taskRecord, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "generate_epub", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"generate_epub"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	workPath := filepath.Join(root, "work", taskRecord.ID, "output.epub.part")
	if err := os.MkdirAll(filepath.Dir(workPath), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("complete bytes before rename")
	if err := os.WriteFile(workPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest, _, err := hashFile(workPath)
	if err != nil {
		t.Fatal(err)
	}
	finalRel := filepath.ToSlash(filepath.Join("artifacts", book.ID, "never-renamed.epub"))
	if _, err := storage.db.Exec(`INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, source_file_id, task_id, created_at) VALUES('before-rename', ?, 'artifact', 'pending', 'epub', 'book.epub', ?, ?, ?, ?, ?, ?)`, book.ID, finalRel, digest, len(data), book.Original.ID, taskRecord.ID, formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForWorker(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RecoverRunningTasks(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reopened.File(context.Background(), "before-rename"); err != nil || ok {
		t.Fatalf("rolled-back file visible = %v, %v", ok, err)
	}
	recoveredTask, ok, err := reopened.Task(context.Background(), taskRecord.ID)
	if err != nil || !ok || recoveredTask.Status != "failed" || recoveredTask.ErrorCode != "task_process_interrupted" {
		t.Fatalf("recovered task = %#v, %v, %v", recoveredTask, ok, err)
	}
	if _, err := os.Stat(workPath); !os.IsNotExist(err) {
		t.Fatalf("interrupted work remains: %v", err)
	}
}

func TestStartupFailsRenamedRevisionDeliverableGroup(t *testing.T) {
	root := t.TempDir()
	storage, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	taskRecord, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "build_revision_txt", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"build_revision_txt"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	finalDirRel := filepath.ToSlash(filepath.Join("revisions", book.ID, "revision-file"))
	finalDir, err := storage.ResolveRel(finalDirRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	values := []struct{ id, role, format, name, data string }{
		{id: "revision-file", role: "revision", format: "txt", name: "revision.txt", data: "revised"},
		{id: "report-file", role: "report", format: "md", name: "report.md", data: "report"},
		{id: "audit-file", role: "audit", format: "jsonl", name: "audit.jsonl", data: "{}\n"},
	}
	for _, value := range values {
		path := filepath.Join(finalDir, value.name)
		if err := os.WriteFile(path, []byte(value.data), 0o600); err != nil {
			t.Fatal(err)
		}
		digest, size, err := hashFile(path)
		if err != nil {
			t.Fatal(err)
		}
		relPath := filepath.ToSlash(filepath.Join(finalDirRel, value.name))
		if _, err := storage.db.Exec(`INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, source_file_id, task_id, created_at) VALUES(?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?)`, value.id, book.ID, value.role, value.format, value.name, relPath, digest, size, book.Original.ID, taskRecord.ID, formatTime(time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenForWorker(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.RecoverRunningTasks(context.Background(), time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	files, err := reopened.FilesForBook(context.Background(), book.ID)
	if err != nil || len(files) != 1 || files[0].Role != "original" {
		t.Fatalf("interrupted revision files = %#v, %v", files, err)
	}
	recoveredTask, ok, err := reopened.Task(context.Background(), taskRecord.ID)
	if err != nil || !ok || recoveredTask.Status != "failed" || recoveredTask.ErrorCode != "task_process_interrupted" {
		t.Fatalf("recovered revision task = %#v, %v, %v", recoveredTask, ok, err)
	}
}

func TestOpenRemovesAbandonedIncomingFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "incoming"), 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(root, "incoming", "abandoned.part")
	if err := os.WriteFile(part, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("abandoned incoming file still exists: %v", err)
	}
}

func TestOpenForWorkerDefersCleanupUntilRuntimeInitialization(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "incoming"), 0o755); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(root, "incoming", "pending-confirmation.part")
	if err := os.WriteFile(part, []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := OpenForWorker(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := os.Stat(part); err != nil {
		t.Fatalf("worker open cleaned incoming before lock: %v", err)
	}
	if err := store.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("runtime initialization left incoming file: %v", err)
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

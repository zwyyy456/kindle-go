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

func TestDeleteBookBlocksActiveTasksThenCascadesFiles(t *testing.T) {
	storage, book := deletionFixture(t)
	task, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "generate_epub", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	err = storage.DeleteBookAggregate(context.Background(), book.ID)
	var active *ActiveTasksError
	if !errors.As(err, &active) || active.Count != 1 {
		t.Fatalf("active task deletion error = %v", err)
	}
	if _, changed, err := storage.CancelTask(context.Background(), task.ID, time.Now()); err != nil || !changed {
		t.Fatalf("cancel = %v, %v", changed, err)
	}
	originalPath, err := storage.ResolveRel(book.Original.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteBookAggregate(context.Background(), book.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("deleted original still exists: %v", err)
	}
	if _, ok, err := storage.Book(context.Background(), book.ID); err != nil || ok {
		t.Fatalf("deleted book = ok %v, err %v", ok, err)
	}
}

func TestOpenCompletesInterruptedBookDeletion(t *testing.T) {
	root := t.TempDir()
	storage, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	originalPath, _ := storage.ResolveRel(book.Original.RelPath)
	proofreadStatePath := committedProofreadState(t, storage, book)
	if _, err := storage.beginBookDeletion(context.Background(), book.ID); err != nil {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := os.Stat(originalPath); !os.IsNotExist(err) {
		t.Fatalf("interrupted deletion left original: %v", err)
	}
	if _, err := os.Stat(proofreadStatePath); !os.IsNotExist(err) {
		t.Fatalf("interrupted deletion left proofread state: %v", err)
	}
	if _, ok, err := reopened.Book(context.Background(), book.ID); err != nil || ok {
		t.Fatalf("interrupted deletion left book: %v, %v", ok, err)
	}
}

func TestDeleteBookRemovesProofreadEngineState(t *testing.T) {
	storage, book := deletionFixture(t)
	statePath := committedProofreadState(t, storage, book)
	if err := storage.DeleteBookAggregate(context.Background(), book.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("deleted proofread state still exists: %v", err)
	}
}

func committedProofreadState(t *testing.T, storage *Store, book Book) string {
	t.Helper()
	created, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "proofread", InputFileID: book.Original.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"proofread"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	workRel := filepath.ToSlash(filepath.Join("work", created.ID, "proofread-state"))
	workPath, err := storage.ResolveRel(workRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workPath, "candidates.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runID, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.CommitProofreadRun(context.Background(), ProofreadRunRecord{
		ID: runID, BookID: book.ID, SourceFileID: book.Original.ID, TaskID: created.ID,
		SourceSHA256: book.Original.SHA256, Format: "txt", BatchSize: 12000, Concurrency: 3,
		EngineVersion: "test", CreatedAt: time.Now(),
	}, nil, workRel, time.Now()); err != nil {
		t.Fatal(err)
	}
	run, ok, err := storage.ProofreadRun(context.Background(), runID)
	if err != nil || !ok {
		t.Fatalf("proofread run = %#v, %v, %v", run, ok, err)
	}
	statePath, err := storage.ResolveRel(run.EngineStateRelPath)
	if err != nil {
		t.Fatal(err)
	}
	return statePath
}

func TestDeleteArtifactPreservesOriginalAndBook(t *testing.T) {
	storage, book := deletionFixture(t)
	artifactID := "artifact-to-delete"
	relPath := filepath.ToSlash(filepath.Join("artifacts", book.ID, artifactID+".epub"))
	path, err := storage.ResolveRel(relPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte("artifact")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	digest, _, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.db.Exec(`INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, created_at) VALUES(?, ?, 'artifact', 'ready', 'epub', 'book.epub', ?, ?, ?, ?)`, artifactID, book.ID, relPath, digest, len(data), formatTime(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteDerivedFile(context.Background(), book.Original.ID); err == nil {
		t.Fatal("original was deletable as an artifact")
	}
	if err := storage.DeleteDerivedFile(context.Background(), artifactID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("artifact still exists: %v", err)
	}
	loaded, ok, err := storage.Book(context.Background(), book.ID)
	if err != nil || !ok || loaded.Original.ID != book.Original.ID {
		t.Fatalf("book after artifact deletion = %#v, %v, %v", loaded, ok, err)
	}
}

func TestDeleteReferencedRevisionIsRejected(t *testing.T) {
	storage, book := deletionFixture(t)

	revisionTask, err := storage.CreateTask(context.Background(), CreateTaskParams{
		BookID: book.ID, Type: "build_revision_txt", InputFileID: book.Original.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"build_revision_txt"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim revision = %v, %v", ok, err)
	}
	revisionWorkRel := filepath.ToSlash(filepath.Join("work", revisionTask.ID, "revision-output"))
	revisionWorkPath, err := storage.ResolveRel(revisionWorkRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(revisionWorkPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"revision.txt": "修订内容",
		"report.md":    "报告",
		"audit.jsonl":  "{}\n",
	} {
		if err := os.WriteFile(filepath.Join(revisionWorkPath, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	revisionResult, err := storage.CommitRevision(context.Background(), RevisionCommit{
		BookID: book.ID, SourceFileID: book.Original.ID, TaskID: revisionTask.ID,
		Format: "txt", RevisionName: "book-revised.txt", ReportName: "book-report.md", AuditName: "book-audit.jsonl",
		WorkDirRel: revisionWorkRel, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	artifactTask, err := storage.CreateTask(context.Background(), CreateTaskParams{
		BookID: book.ID, Type: "generate_epub", InputFileID: revisionResult.Revision.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := storage.ClaimNextTask(context.Background(), []string{"generate_epub"}, time.Now()); err != nil || !ok {
		t.Fatalf("claim artifact = %v, %v", ok, err)
	}
	artifactWorkRel := filepath.ToSlash(filepath.Join("work", artifactTask.ID, "artifact.epub"))
	artifactWorkPath, err := storage.ResolveRel(artifactWorkRel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(artifactWorkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactWorkPath, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifact, err := storage.CommitArtifact(context.Background(), ArtifactCommit{
		BookID: book.ID, SourceFileID: revisionResult.Revision.ID, TaskID: artifactTask.ID,
		Format: "epub", DisplayName: "book.epub", WorkRelPath: artifactWorkRel, CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = storage.DeleteDerivedFile(context.Background(), revisionResult.Revision.ID)
	var referenced *ReferencedFileError
	if !errors.As(err, &referenced) || referenced.FileID != revisionResult.Revision.ID || referenced.Count != 1 {
		t.Fatalf("delete referenced revision error = %v", err)
	}
	for _, id := range []string{revisionResult.Revision.ID, artifact.ID} {
		file, ok, err := storage.File(context.Background(), id)
		if err != nil || !ok || file.State != "ready" {
			t.Fatalf("referenced file %s after rejected deletion = %#v, %v, %v", id, file, ok, err)
		}
	}
}

func TestDeleteBookDoesNotTouchExternalImportSource(t *testing.T) {
	externalDir := t.TempDir()
	externalPath := filepath.Join(externalDir, "outside.txt")
	if err := os.WriteFile(externalPath, []byte("external source"), 0o644); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(externalPath)
	if err != nil {
		t.Fatal(err)
	}
	storage, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	book, err := storage.CreateOriginal(context.Background(), "outside.txt", "outside.txt", "txt", handle, time.Now())
	_ = handle.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteBookAggregate(context.Background(), book.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(externalPath)
	if err != nil || string(data) != "external source" {
		t.Fatalf("external source changed: %q, %v", data, err)
	}
}

func deletionFixture(t *testing.T) (*Store, Book) {
	t.Helper()
	storage, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("source"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return storage, book
}

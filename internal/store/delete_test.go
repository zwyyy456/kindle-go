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
	err = storage.DeleteBook(context.Background(), book.ID)
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
	if err := storage.DeleteBook(context.Background(), book.ID); err != nil {
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
	if _, ok, err := reopened.Book(context.Background(), book.ID); err != nil || ok {
		t.Fatalf("interrupted deletion left book: %v, %v", ok, err)
	}
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
	if err := storage.DeleteArtifact(context.Background(), book.Original.ID); err == nil {
		t.Fatal("original was deletable as an artifact")
	}
	if err := storage.DeleteArtifact(context.Background(), artifactID); err != nil {
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
	if err := storage.DeleteBook(context.Background(), book.ID); err != nil {
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

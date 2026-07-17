package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticsReportsDatabaseFilesystemAndExecutionSlots(t *testing.T) {
	storage, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	book, err := storage.CreateOriginal(context.Background(), "book.txt", "book.txt", "txt", strings.NewReader("正文"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "generate_epub", InputFileID: book.Original.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := storage.CreateTask(context.Background(), CreateTaskParams{BookID: book.ID, Type: "proofread", InputFileID: book.Original.ID}); err != nil {
		t.Fatal(err)
	}
	result, err := storage.Diagnostics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != schemaVersion || strings.ToLower(result.JournalMode) != "wal" || !result.LibraryWritable || result.FreeBytes == 0 || result.QueuedGeneration != 1 || result.QueuedProofread != 1 {
		t.Fatalf("diagnostics = %#v", result)
	}
}

func TestInitializeRemovesOrphanTaskWork(t *testing.T) {
	root := t.TempDir()
	storage, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "work", "orphan-task", "partial")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "output.part"), []byte("partial"), 0o600); err != nil {
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
	if _, err := os.Stat(filepath.Join(root, "work", "orphan-task")); !os.IsNotExist(err) {
		t.Fatalf("orphan work remains: %v", err)
	}
}

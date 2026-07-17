package server

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLibraryRecentUsesUploadTime(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2026, 7, 1, 11, 59, 0, 0, time.UTC)
	recentTime := time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC)
	if _, err := library.AddUpload("old.txt", strings.NewReader("old"), oldTime); err != nil {
		t.Fatal(err)
	}
	if _, err := library.AddUpload("recent.txt", strings.NewReader("recent"), recentTime); err != nil {
		t.Fatal(err)
	}

	records := library.Recent(time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), 24*time.Hour)
	if len(records) != 1 {
		t.Fatalf("got %d recent records, want 1", len(records))
	}
	if records[0].OriginalName != "recent.txt" {
		t.Fatalf("got %q, want recent.txt", records[0].OriginalName)
	}
}

func TestLibraryDoesNotOverwriteSameFileName(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := library.AddUpload("book.txt", strings.NewReader("one"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := library.AddUpload("book.txt", strings.NewReader("two"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.Original.RelPath == second.Original.RelPath {
		t.Fatalf("uploads used same path %q", first.Original.RelPath)
	}
}

func TestLibraryResolveRejectsUnsafeIndexPath(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.resolveRel("../bad.txt"); err == nil {
		t.Fatal("expected unsafe path error")
	}
}

func TestLibraryResolveOutputUsesCurrentOutput(t *testing.T) {
	library, err := NewLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	record, err := library.AddUpload("book.txt", strings.NewReader("book"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	path, relPath, err := library.ConvertedPath(record.ID, "book-new.azw3")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("output"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := library.AddConverted(record.ID, "book-new.azw3", relPath, int64(len("output")), created); err != nil {
		t.Fatal(err)
	}

	_, file, err := library.ResolveFile(record.ID, "output")
	if err != nil {
		t.Fatal(err)
	}
	if file.Name != "book-new.azw3" {
		t.Fatalf("got %q, want latest output", file.Name)
	}
}

func TestKindleFormatExcludesEPUB(t *testing.T) {
	if kindleFormat("epub") {
		t.Fatal("epub should not be shown on Kindle page")
	}
	for _, format := range []string{"azw3", "mobi", "pdf", "txt"} {
		if !kindleFormat(format) {
			t.Fatalf("%s should be shown on Kindle page", format)
		}
	}
}

package library

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

func TestImportEnforcesExactTXTLimitWithoutResidue(t *testing.T) {
	service, root := newTestService(t)
	service.maxTXTBytes = 4

	result, err := service.Import(context.Background(), ImportRequest{Filename: "book.txt", Reader: strings.NewReader("1234")})
	if err != nil || result.Book.ID == "" {
		t.Fatalf("boundary import = %#v, %v", result, err)
	}
	_, err = service.Import(context.Background(), ImportRequest{Filename: "large.txt", Reader: strings.NewReader("12345")})
	if ErrorCode(err) != "upload_too_large" {
		t.Fatalf("oversize error = %v", err)
	}
	books, err := service.AllBooks(context.Background())
	if err != nil || len(books) != 1 {
		t.Fatalf("books after rejected import = %d, %v", len(books), err)
	}
	assertIncomingCount(t, root, 0)
}

func TestImportRejectsUnsupportedAndMismatchedContent(t *testing.T) {
	service, root := newTestService(t)
	for _, test := range []struct {
		name, filename, content string
	}{
		{name: "extension", filename: "book.pdf", content: "pdf"},
		{name: "epub content", filename: "book.epub", content: "not a zip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.Import(context.Background(), ImportRequest{Filename: test.filename, Reader: strings.NewReader(test.content)})
			if ErrorCode(err) != "unsupported_format" {
				t.Fatalf("error = %v", err)
			}
		})
	}
	assertIncomingCount(t, root, 0)
}

func TestImportChecksEPUBDeclaredExpandedSize(t *testing.T) {
	service, root := newTestService(t)
	service.maxEPUBExpandedBytes = 16
	data := epubArchive(t, map[string]string{
		"META-INF/container.xml": "container",
		"book.xhtml":             strings.Repeat("x", 17),
	})
	_, err := service.Import(context.Background(), ImportRequest{Filename: "book.epub", Reader: bytes.NewReader(data)})
	if ErrorCode(err) != "epub_expanded_too_large" {
		t.Fatalf("expanded size error = %v", err)
	}
	assertIncomingCount(t, root, 0)
}

func TestDuplicateImportCanOpenExistingOrCreateNewBook(t *testing.T) {
	service, root := newTestService(t)
	clock := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	first, err := service.Import(context.Background(), ImportRequest{Filename: "book.txt", Reader: strings.NewReader("same")})
	if err != nil {
		t.Fatal(err)
	}

	duplicate, err := service.Import(context.Background(), ImportRequest{Filename: "copy.txt", Reader: strings.NewReader("same")})
	if err != nil || !duplicate.Duplicate || duplicate.DuplicateToken == "" || len(duplicate.Existing) != 1 {
		t.Fatalf("duplicate = %#v, %v", duplicate, err)
	}
	assertIncomingCount(t, root, 1)
	opened, err := service.ConfirmImport(context.Background(), duplicate.DuplicateToken, "open")
	if err != nil || opened.ID != first.Book.ID {
		t.Fatalf("opened = %#v, %v", opened, err)
	}
	assertIncomingCount(t, root, 0)

	duplicate, err = service.Import(context.Background(), ImportRequest{Filename: "copy.txt", Reader: strings.NewReader("same")})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.ConfirmImport(context.Background(), duplicate.DuplicateToken, "import")
	if err != nil || created.ID == first.Book.ID {
		t.Fatalf("explicit duplicate = %#v, %v", created, err)
	}
	books, err := service.AllBooks(context.Background())
	if err != nil || len(books) != 2 {
		t.Fatalf("books = %d, %v", len(books), err)
	}
}

func TestDuplicateTokenExpiresAndCancelDiscardsIncoming(t *testing.T) {
	service, root := newTestService(t)
	clock := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }
	if _, err := service.Import(context.Background(), ImportRequest{Filename: "book.txt", Reader: strings.NewReader("same")}); err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.Import(context.Background(), ImportRequest{Filename: "copy.txt", Reader: strings.NewReader("same")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmImport(context.Background(), duplicate.DuplicateToken, "cancel"); err != nil {
		t.Fatal(err)
	}
	assertIncomingCount(t, root, 0)

	duplicate, err = service.Import(context.Background(), ImportRequest{Filename: "copy.txt", Reader: strings.NewReader("same")})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(duplicateTokenTTL + time.Second)
	_, err = service.ConfirmImport(context.Background(), duplicate.DuplicateToken, "import")
	var tokenErr *DuplicateTokenError
	if !errors.As(err, &tokenErr) {
		t.Fatalf("expired token error = %v", err)
	}
	assertIncomingCount(t, root, 0)
}

func TestSameNameDifferentContentIsNotDuplicate(t *testing.T) {
	service, _ := newTestService(t)
	for _, content := range []string{"one", "two"} {
		result, err := service.Import(context.Background(), ImportRequest{Filename: "book.txt", Reader: strings.NewReader(content)})
		if err != nil || result.Duplicate {
			t.Fatalf("import %q = %#v, %v", content, result, err)
		}
	}
}

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	storage, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	service := New(storage)
	t.Cleanup(func() { _ = service.Close() })
	return service, root
}

func assertIncomingCount(t *testing.T, root string, want int) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "incoming"))
	if os.IsNotExist(err) {
		entries = nil
	} else if err != nil {
		t.Fatal(err)
	}
	if len(entries) != want {
		t.Fatalf("incoming files = %d, want %d", len(entries), want)
	}
}

func epubArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o644)
		entry, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

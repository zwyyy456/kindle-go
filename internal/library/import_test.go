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
	"github.com/flashdict/kindle2flashdict/internal/task"
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

func TestImportEnforcesExactEPUBLimitWithoutResidue(t *testing.T) {
	service, root := newTestService(t)
	data := epubArchive(t, map[string]string{
		"META-INF/container.xml": "container",
		"book.xhtml":             "content",
	})
	service.maxEPUBBytes = int64(len(data))
	result, err := service.Import(context.Background(), ImportRequest{Filename: "boundary.epub", Reader: bytes.NewReader(data)})
	if err != nil || result.Book.ID == "" {
		t.Fatalf("boundary import = %#v, %v", result, err)
	}
	service.maxEPUBBytes = int64(len(data) - 1)
	_, err = service.Import(context.Background(), ImportRequest{Filename: "large.epub", Reader: bytes.NewReader(data)})
	if ErrorCode(err) != "upload_too_large" {
		t.Fatalf("oversize error = %v", err)
	}
	books, err := service.AllBooks(context.Background())
	if err != nil || len(books) != 1 {
		t.Fatalf("books after rejected import = %d, %v", len(books), err)
	}
	assertIncomingCount(t, root, 0)
}

func TestImportChecksEPUBDeclaredExpandedSize(t *testing.T) {
	service, root := newTestService(t)
	containerXML := `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="missing.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`
	service.maxEPUBExpandedBytes = int64(len(containerXML) + 16)
	boundary := epubArchive(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"a":                      strings.Repeat("x", 16),
	})
	result, err := service.Import(context.Background(), ImportRequest{Filename: "boundary.epub", Reader: bytes.NewReader(boundary)})
	if err != nil || result.Book.ID == "" {
		t.Fatalf("expanded boundary import = %#v, %v", result, err)
	}
	data := epubArchive(t, map[string]string{
		"META-INF/container.xml": containerXML,
		"a":                      strings.Repeat("x", 17),
	})
	_, err = service.Import(context.Background(), ImportRequest{Filename: "book.epub", Reader: bytes.NewReader(data)})
	if ErrorCode(err) != "epub_expanded_too_large" {
		t.Fatalf("expanded size error = %v", err)
	}
	assertIncomingCount(t, root, 0)
}

func TestEPUBImportPersistsPassedAndFailedCompatibilityReports(t *testing.T) {
	service, _ := newTestService(t)
	valid := epubArchive(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"book.opf":               `<package xmlns="http://www.idpf.org/2007/opf"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>兼容书</dc:title></metadata><manifest><item id="c" href="c.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c"/></spine></package>`,
		"c.xhtml":                `<html xmlns="http://www.w3.org/1999/xhtml"><body><p>正文</p></body></html>`,
	})
	passed, err := service.Import(context.Background(), ImportRequest{Filename: "passed.epub", Reader: bytes.NewReader(valid)})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok, err := service.GetBookDetail(context.Background(), passed.Book.ID)
	if err != nil || !ok || detail.Compatibility == nil || detail.Compatibility.Status != "passed" || detail.Compatibility.Metadata.Title != "兼容书" {
		t.Fatalf("passed report = %#v, %v, %v", detail.Compatibility, ok, err)
	}
	invalid := epubArchive(t, map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="missing.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
	})
	failed, err := service.Import(context.Background(), ImportRequest{Filename: "failed.epub", Reader: bytes.NewReader(invalid)})
	if err != nil {
		t.Fatal(err)
	}
	detail, ok, err = service.GetBookDetail(context.Background(), failed.Book.ID)
	if err != nil || !ok || detail.Compatibility == nil || detail.Compatibility.Status != "failed" || len(detail.Compatibility.Issues) == 0 || detail.Compatibility.Issues[0].Code == "" {
		t.Fatalf("failed report = %#v, %v, %v", detail.Compatibility, ok, err)
	}
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

func TestListBooksUsesStablePaginationSortAndLiteralSearch(t *testing.T) {
	service, _ := newTestService(t)
	for index, value := range []struct{ name, content string }{
		{name: "Charlie.txt", content: "three"},
		{name: "Alpha.txt", content: "one"},
		{name: "100%.txt", content: "percent"},
	} {
		_, err := service.Import(context.Background(), ImportRequest{
			Filename: value.name, Reader: strings.NewReader(value.content),
			Now: time.Date(2026, 7, 17, 8, index, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.ListBooks(context.Background(), BookQuery{Sort: "name_asc", Page: 1, PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 3 || len(first.Books) != 2 || first.Books[0].DisplayName != "100%.txt" || first.Books[1].DisplayName != "Alpha.txt" || !first.HasNext || first.HasPrevious {
		t.Fatalf("first page = %#v", first)
	}
	second, err := service.ListBooks(context.Background(), BookQuery{Sort: "name_asc", Page: 2, PageSize: 2})
	if err != nil || len(second.Books) != 1 || second.Books[0].DisplayName != "Charlie.txt" || !second.HasPrevious || second.HasNext {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	search, err := service.ListBooks(context.Background(), BookQuery{Search: "%", PageSize: 50})
	if err != nil || search.Total != 1 || search.Books[0].DisplayName != "100%.txt" {
		t.Fatalf("literal search = %#v, %v", search, err)
	}
}

func TestListBooksFiltersDerivedProofreadStatus(t *testing.T) {
	service, _ := newTestService(t)
	first, err := service.Import(context.Background(), ImportRequest{Filename: "one.txt", Reader: strings.NewReader("one")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Import(context.Background(), ImportRequest{Filename: "two.txt", Reader: strings.NewReader("two")})
	if err != nil {
		t.Fatal(err)
	}
	taskService := task.NewService(service.store)
	if _, err := taskService.Create(context.Background(), task.CreateRequest{BookID: second.Book.ID, Type: task.Proofread, InputFileID: second.Book.Original.ID}); err != nil {
		t.Fatal(err)
	}
	queued, err := service.ListBooks(context.Background(), BookQuery{StatusFilter: "queued"})
	if err != nil || queued.Total != 1 || queued.Books[0].ID != second.Book.ID || queued.Books[0].ProofreadStatus != "queued" {
		t.Fatalf("queued page = %#v, %v", queued, err)
	}
	notStarted, err := service.ListBooks(context.Background(), BookQuery{StatusFilter: "not_started"})
	if err != nil || notStarted.Total != 1 || notStarted.Books[0].ID != first.Book.ID || notStarted.Books[0].ProofreadStatus != "not_started" {
		t.Fatalf("not-started page = %#v, %v", notStarted, err)
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

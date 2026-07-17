package library

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

const duplicateTokenTTL = 15 * time.Minute

type pendingImport struct {
	incoming  store.IncomingFile
	filename  string
	format    string
	existing  []Book
	expiresAt time.Time
}

type Service struct {
	store                *store.Store
	now                  func() time.Time
	maxTXTBytes          int64
	maxEPUBBytes         int64
	maxEPUBExpandedBytes int64

	mu      sync.Mutex
	pending map[string]pendingImport
}

func New(source *store.Store) *Service {
	return &Service{
		store: source, now: time.Now, pending: make(map[string]pendingImport),
		maxTXTBytes: MaxTXTBytes, maxEPUBBytes: MaxEPUBBytes, maxEPUBExpandedBytes: MaxEPUBExpandedBytes,
	}
}

func Open(root string) (*Service, error) {
	storage, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	return New(storage), nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	for token, pending := range s.pending {
		_ = s.store.DiscardIncoming(pending.incoming)
		delete(s.pending, token)
	}
	s.mu.Unlock()
	return s.store.Close()
}

func (s *Service) Root() string { return s.store.Root() }

func (s *Service) GetBook(ctx context.Context, id string) (Book, bool, error) {
	book, ok, err := s.store.Book(ctx, id)
	return bookFromStore(book), ok, err
}

func (s *Service) AllBooks(ctx context.Context) ([]Book, error) {
	books, err := s.store.AllBooks(ctx)
	return booksFromStore(books), err
}

func (s *Service) RecentBooks(ctx context.Context, cutoff time.Time) ([]Book, error) {
	books, err := s.store.RecentBooks(ctx, cutoff)
	return booksFromStore(books), err
}

func (s *Service) ResolveFile(ctx context.Context, bookID, kind string) (string, File, error) {
	book, ok, err := s.store.Book(ctx, bookID)
	if err != nil {
		return "", File{}, err
	}
	if !ok {
		return "", File{}, os.ErrNotExist
	}
	var value store.File
	switch kind {
	case "original":
		value = book.Original
	case "output":
		value = book.LatestArtifact
	default:
		return "", File{}, fmt.Errorf("unknown file kind %q", kind)
	}
	if value.ID == "" {
		return "", File{}, os.ErrNotExist
	}
	path, err := s.store.ResolveRel(value.RelPath)
	return path, fileFromStore(value), err
}

func booksFromStore(values []store.Book) []Book {
	books := make([]Book, 0, len(values))
	for _, value := range values {
		books = append(books, bookFromStore(value))
	}
	return books
}

func bookFromStore(value store.Book) Book {
	return Book{
		ID: value.ID, DisplayName: value.DisplayName, SourceFormat: value.SourceFormat, ImportedAt: value.ImportedAt,
		LegacyLastError: value.LegacyLastError,
		Original:        fileFromStore(value.Original), LatestArtifact: fileFromStore(value.LatestArtifact),
	}
}

func fileFromStore(value store.File) File {
	return File{ID: value.ID, BookID: value.BookID, Role: value.Role, Format: value.Format, DisplayName: value.DisplayName, SHA256: value.SHA256, Size: value.Size, CreatedAt: value.CreatedAt}
}

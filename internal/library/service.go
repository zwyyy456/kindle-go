package library

import (
	"context"
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

func (s *Service) GetBookDetail(ctx context.Context, id string) (BookDetail, bool, error) {
	book, ok, err := s.GetBook(ctx, id)
	if err != nil || !ok {
		return BookDetail{}, ok, err
	}
	files, err := s.store.FilesForBook(ctx, id)
	if err != nil {
		return BookDetail{}, false, err
	}
	projection := make([]File, 0, len(files))
	for _, file := range files {
		projection = append(projection, fileFromStore(file))
	}
	return BookDetail{Book: book, Files: projection}, true, nil
}

func (s *Service) DownloadFile(ctx context.Context, id string) (string, File, error) {
	file, ok, err := s.store.File(ctx, id)
	if err != nil {
		return "", File{}, err
	}
	if !ok {
		return "", File{}, os.ErrNotExist
	}
	path, err := s.store.ResolveRel(file.RelPath)
	return path, fileFromStore(file), err
}

func (s *Service) LatestKindleFiles(ctx context.Context, showEPUB bool) ([]KindleBook, error) {
	books, err := s.store.AllBooks(ctx)
	if err != nil {
		return nil, err
	}
	var result []KindleBook
	for _, storedBook := range books {
		files, err := s.store.FilesForBook(ctx, storedBook.ID)
		if err != nil {
			return nil, err
		}
		var selected store.File
		for _, file := range files {
			if file.Role == "artifact" && file.Format == "azw3" {
				selected = file
				break
			}
		}
		if selected.ID == "" && showEPUB {
			for _, file := range files {
				if (file.Role == "artifact" || file.Role == "revision") && file.Format == "epub" {
					selected = file
					break
				}
			}
		}
		if selected.ID == "" && kindleOriginalFormat(storedBook.Original.Format) {
			selected = storedBook.Original
		}
		if selected.ID != "" {
			result = append(result, KindleBook{Book: bookFromStore(storedBook), File: fileFromStore(selected)})
		}
	}
	return result, nil
}

func (s *Service) AllBooks(ctx context.Context) ([]Book, error) {
	books, err := s.store.AllBooks(ctx)
	return booksFromStore(books), err
}

func (s *Service) RecentBooks(ctx context.Context, cutoff time.Time) ([]Book, error) {
	books, err := s.store.RecentBooks(ctx, cutoff)
	return booksFromStore(books), err
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
	return File{
		ID: value.ID, BookID: value.BookID, Role: value.Role, Format: value.Format, DisplayName: value.DisplayName,
		SHA256: value.SHA256, Size: value.Size, SourceFileID: value.SourceFileID, TaskID: value.TaskID,
		ParametersJSON: value.ParametersJSON, HasUnresolved: value.HasUnresolved, CreatedAt: value.CreatedAt,
	}
}

func kindleOriginalFormat(format string) bool {
	switch format {
	case "azw3", "mobi", "pdf", "txt":
		return true
	default:
		return false
	}
}

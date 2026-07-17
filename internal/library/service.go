package library

import (
	"context"
	"encoding/json"
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
	detail := BookDetail{Book: book, Files: projection}
	if book.SourceFormat == "epub" {
		record, found, err := s.store.Compatibility(ctx, book.Original.ID)
		if err != nil {
			return BookDetail{}, false, err
		}
		if found {
			report := CompatibilityReport{FileID: record.FileID, SourceSHA256: record.SourceSHA256, Status: record.Status, CheckedAt: record.CheckedAt}
			var metadata struct {
				Metadata json.RawMessage `json:"metadata"`
				Cover    json.RawMessage `json:"cover"`
			}
			if err := json.Unmarshal([]byte(record.MetadataJSON), &metadata); err != nil {
				return BookDetail{}, false, err
			}
			if err := json.Unmarshal(metadata.Metadata, &report.Metadata); err != nil {
				return BookDetail{}, false, err
			}
			if err := json.Unmarshal(metadata.Cover, &report.Cover); err != nil {
				return BookDetail{}, false, err
			}
			if err := json.Unmarshal([]byte(record.SpineJSON), &report.Spine); err != nil {
				return BookDetail{}, false, err
			}
			if err := json.Unmarshal([]byte(record.TOCJSON), &report.TOC); err != nil {
				return BookDetail{}, false, err
			}
			if err := json.Unmarshal([]byte(record.ResourcesJSON), &report.Resources); err != nil {
				return BookDetail{}, false, err
			}
			if err := json.Unmarshal([]byte(record.IssuesJSON), &report.Issues); err != nil {
				return BookDetail{}, false, err
			}
			detail.Compatibility = &report
		}
	}
	return detail, true, nil
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

func (s *Service) DeleteBook(ctx context.Context, id string) error {
	return s.store.DeleteBook(ctx, id)
}

func (s *Service) DeleteFile(ctx context.Context, id string) error {
	return s.store.DeleteArtifact(ctx, id)
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

func (s *Service) ListBooks(ctx context.Context, query BookQuery) (BookPage, error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	books, total, err := s.store.BooksPage(ctx, query.Search, query.Sort, pageSize, (page-1)*pageSize)
	if err != nil {
		return BookPage{}, err
	}
	return BookPage{
		Books: booksFromStore(books), Page: page, PageSize: pageSize, Total: total,
		HasPrevious: page > 1, HasNext: page*pageSize < total,
	}, nil
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

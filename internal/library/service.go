package library

import (
	"context"
	"encoding/json"
	"errors"
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

// Close discards pending imports owned by the service. The caller that opened
// the backing Store remains responsible for closing it after the service.
func (s *Service) Close() error {
	s.mu.Lock()
	var cleanupErr error
	for token, pending := range s.pending {
		cleanupErr = errors.Join(cleanupErr, s.store.DiscardIncoming(pending.incoming))
		delete(s.pending, token)
	}
	s.mu.Unlock()
	return cleanupErr
}

func (s *Service) GetBook(ctx context.Context, id string) (Book, bool, error) {
	book, ok, err := s.store.Book(ctx, id)
	return bookFromStore(book), ok, err
}

func (s *Service) GetFile(ctx context.Context, id string) (File, bool, error) {
	file, ok, err := s.store.File(ctx, id)
	return fileFromStore(file), ok, err
}

func (s *Service) Diagnostics(ctx context.Context) (Diagnostics, error) {
	value, err := s.store.Diagnostics(ctx)
	if err != nil {
		return Diagnostics{}, err
	}
	return Diagnostics{
		SchemaVersion: value.SchemaVersion, JournalMode: value.JournalMode,
		LibraryWritable: value.LibraryWritable, FreeBytes: value.FreeBytes,
		QueuedGeneration: value.QueuedGeneration, RunningGeneration: value.RunningGeneration,
		QueuedProofread: value.QueuedProofread, RunningProofread: value.RunningProofread,
	}, nil
}

func (s *Service) CompatibilityForFile(ctx context.Context, fileID string) (CompatibilityReport, bool, error) {
	record, found, err := s.store.Compatibility(ctx, fileID)
	if err != nil || !found {
		return CompatibilityReport{}, found, err
	}
	report := CompatibilityReport{FileID: record.FileID, SourceSHA256: record.SourceSHA256, Status: record.Status, CheckedAt: record.CheckedAt}
	var metadata struct {
		Metadata json.RawMessage `json:"metadata"`
		Cover    json.RawMessage `json:"cover"`
	}
	if err := json.Unmarshal([]byte(record.MetadataJSON), &metadata); err != nil {
		return CompatibilityReport{}, false, err
	}
	if err := json.Unmarshal(metadata.Metadata, &report.Metadata); err != nil {
		return CompatibilityReport{}, false, err
	}
	if err := json.Unmarshal(metadata.Cover, &report.Cover); err != nil {
		return CompatibilityReport{}, false, err
	}
	if err := json.Unmarshal([]byte(record.SpineJSON), &report.Spine); err != nil {
		return CompatibilityReport{}, false, err
	}
	if err := json.Unmarshal([]byte(record.TOCJSON), &report.TOC); err != nil {
		return CompatibilityReport{}, false, err
	}
	if err := json.Unmarshal([]byte(record.ResourcesJSON), &report.Resources); err != nil {
		return CompatibilityReport{}, false, err
	}
	if err := json.Unmarshal([]byte(record.IssuesJSON), &report.Issues); err != nil {
		return CompatibilityReport{}, false, err
	}
	return report, true, nil
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
		report, found, err := s.CompatibilityForFile(ctx, book.Original.ID)
		if err != nil {
			return BookDetail{}, false, err
		}
		if found {
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

func (s *Service) DownloadKindleFile(ctx context.Context, id string, showEPUB bool) (string, File, error) {
	candidate, ok, err := s.store.File(ctx, id)
	if err != nil {
		return "", File{}, err
	}
	if !ok {
		return "", File{}, os.ErrNotExist
	}
	files, err := s.store.FilesForBook(ctx, candidate.BookID)
	if err != nil {
		return "", File{}, err
	}
	for _, file := range kindleFileRecords(files, showEPUB) {
		if file.ID == id {
			path, err := s.store.ResolveRel(file.RelPath)
			return path, fileFromStore(file), err
		}
	}
	return "", File{}, os.ErrNotExist
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
	files, err := s.store.ReadyFilesForActiveBooks(ctx)
	if err != nil {
		return nil, err
	}
	filesByBook := make(map[string][]store.File)
	for _, file := range files {
		filesByBook[file.BookID] = append(filesByBook[file.BookID], file)
	}
	var result []KindleBook
	for _, storedBook := range books {
		selectedRecords := kindleFileRecords(filesByBook[storedBook.ID], showEPUB)
		selected := make([]File, 0, len(selectedRecords))
		for _, file := range selectedRecords {
			selected = append(selected, fileFromStore(file))
		}
		if len(selected) != 0 {
			result = append(result, KindleBook{Book: bookFromStore(storedBook), Files: selected})
		}
	}
	return result, nil
}

func kindleFileRecords(files []store.File, showEPUB bool) []store.File {
	var azw3, epub store.File
	for _, file := range files {
		if azw3.ID == "" && file.Role == "artifact" && file.Format == "azw3" {
			azw3 = file
		}
		if showEPUB && epub.ID == "" && file.Format == "epub" &&
			(file.Role == "artifact" || file.Role == "revision" || file.Role == "original") {
			epub = file
		}
	}
	var selected []store.File
	if azw3.ID != "" {
		selected = append(selected, azw3)
	}
	if epub.ID != "" {
		selected = append(selected, epub)
	}
	return selected
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
	statusFilter := query.StatusFilter
	switch statusFilter {
	case "", "not_started", "queued", "running", "completed", "failed", "canceled":
	default:
		statusFilter = ""
	}
	books, total, err := s.store.BooksPage(ctx, query.Search, query.Sort, statusFilter, pageSize, (page-1)*pageSize)
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
		ProofreadStatus: value.ProofreadStatus,
		Original:        fileFromStore(value.Original), LatestArtifact: fileFromStore(value.LatestArtifact),
	}
}

func fileFromStore(value store.File) File {
	return File{
		ID: value.ID, BookID: value.BookID, Role: value.Role, Format: value.Format, DisplayName: value.DisplayName,
		SHA256: value.SHA256, Size: value.Size, SourceFileID: value.SourceFileID, TaskID: value.TaskID, ProofreadRunID: value.ProofreadRunID,
		ParametersJSON: value.ParametersJSON, HasUnresolved: value.HasUnresolved, CreatedAt: value.CreatedAt,
	}
}

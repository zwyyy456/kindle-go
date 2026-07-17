package library

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/epub"
	"github.com/flashdict/kindle2flashdict/internal/store"
	txttext "github.com/flashdict/kindle2flashdict/internal/txt2epub/text"
)

func (s *Service) Import(ctx context.Context, req ImportRequest) (ImportResult, error) {
	filename := safeFileName(filepath.Base(strings.TrimSpace(req.Filename)))
	format := strings.ToLower(strings.TrimPrefix(filepath.Ext(filename), "."))
	limit, err := s.importLimit(format)
	if err != nil {
		return ImportResult{}, err
	}
	if req.Reader == nil {
		return ImportResult{}, &ImportError{Code: "unsupported_format", Message: "missing import file"}
	}
	clockNow := s.now()
	importedAt := req.Now
	if importedAt.IsZero() {
		importedAt = clockNow
	}
	s.cleanupExpired(clockNow)

	incoming, err := s.store.StageIncoming(io.LimitReader(req.Reader, limit+1))
	if err != nil {
		return ImportResult{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = s.store.DiscardIncoming(incoming)
		}
	}()
	if incoming.Size > limit {
		return ImportResult{}, &ImportError{Code: "upload_too_large", Message: fmt.Sprintf("%s file exceeds the %d MiB limit", strings.ToUpper(format), limit>>20)}
	}
	if err := s.validateIncoming(format, incoming); err != nil {
		return ImportResult{}, err
	}
	existing, err := s.store.OriginalBooksBySHA(ctx, incoming.SHA256)
	if err != nil {
		return ImportResult{}, err
	}
	if len(existing) != 0 {
		token, err := store.NewID()
		if err != nil {
			return ImportResult{}, err
		}
		projection := booksFromStore(existing)
		expiresAt := clockNow.Add(duplicateTokenTTL)
		s.mu.Lock()
		s.pending[token] = pendingImport{incoming: incoming, filename: filename, format: format, existing: projection, expiresAt: expiresAt}
		s.mu.Unlock()
		keep = true
		return ImportResult{Duplicate: true, DuplicateToken: token, Existing: projection, ExpiresAt: expiresAt}, nil
	}
	book, err := s.store.CommitIncomingOriginal(ctx, incoming, filename, filename, format, importedAt)
	if err != nil {
		return ImportResult{}, err
	}
	keep = true
	if err := s.persistCompatibility(ctx, book); err != nil {
		_ = s.store.DeleteBook(context.Background(), book.ID)
		return ImportResult{}, err
	}
	return ImportResult{Book: bookFromStore(book)}, nil
}

func (s *Service) ConfirmImport(ctx context.Context, token, action string) (Book, error) {
	now := s.now()
	s.cleanupExpired(now)
	s.mu.Lock()
	pending, ok := s.pending[token]
	if ok {
		delete(s.pending, token)
	}
	s.mu.Unlock()
	if !ok {
		return Book{}, &DuplicateTokenError{}
	}
	switch action {
	case "open":
		_ = s.store.DiscardIncoming(pending.incoming)
		if len(pending.existing) == 0 {
			return Book{}, &DuplicateTokenError{Message: "duplicate source no longer exists"}
		}
		return pending.existing[0], nil
	case "import":
		book, err := s.store.CommitIncomingOriginal(ctx, pending.incoming, pending.filename, pending.filename, pending.format, now)
		if err != nil {
			_ = s.store.DiscardIncoming(pending.incoming)
			return Book{}, err
		}
		if err := s.persistCompatibility(ctx, book); err != nil {
			_ = s.store.DeleteBook(context.Background(), book.ID)
			return Book{}, err
		}
		return bookFromStore(book), nil
	case "cancel":
		_ = s.store.DiscardIncoming(pending.incoming)
		return Book{}, nil
	default:
		_ = s.store.DiscardIncoming(pending.incoming)
		return Book{}, fmt.Errorf("unknown duplicate import action %q", action)
	}
}

func (s *Service) persistCompatibility(ctx context.Context, book store.Book) error {
	if book.SourceFormat != "epub" {
		return nil
	}
	filename, err := s.store.ResolveRel(book.Original.RelPath)
	if err != nil {
		return err
	}
	analysis := epub.Analyze(filename, epub.Options{DefaultLanguage: "zh-CN"})
	metadata, err := json.Marshal(struct {
		Metadata epub.MetadataInfo `json:"metadata"`
		Cover    epub.CoverInfo    `json:"cover"`
	}{analysis.Metadata, analysis.Cover})
	if err != nil {
		return err
	}
	spine, err := json.Marshal(analysis.Spine)
	if err != nil {
		return err
	}
	toc, err := json.Marshal(analysis.TOC)
	if err != nil {
		return err
	}
	resources, err := json.Marshal(analysis.Resources)
	if err != nil {
		return err
	}
	issues, err := json.Marshal(analysis.Issues)
	if err != nil {
		return err
	}
	status := "passed"
	if !analysis.Compatible() {
		status = "failed"
	}
	return s.store.SaveCompatibility(ctx, store.CompatibilityRecord{FileID: book.Original.ID, SourceSHA256: book.Original.SHA256, Status: status, MetadataJSON: string(metadata), SpineJSON: string(spine), TOCJSON: string(toc), ResourcesJSON: string(resources), IssuesJSON: string(issues), CheckedAt: s.now()})
}

func (s *Service) PendingDuplicate(token string) (PendingDuplicate, bool) {
	now := s.now()
	s.cleanupExpired(now)
	s.mu.Lock()
	defer s.mu.Unlock()
	pending, ok := s.pending[token]
	if !ok {
		return PendingDuplicate{}, false
	}
	return PendingDuplicate{Token: token, Filename: pending.filename, Existing: append([]Book(nil), pending.existing...), ExpiresAt: pending.expiresAt}, true
}

func (s *Service) cleanupExpired(now time.Time) {
	s.mu.Lock()
	var expired []store.IncomingFile
	for token, pending := range s.pending {
		if !now.Before(pending.expiresAt) {
			expired = append(expired, pending.incoming)
			delete(s.pending, token)
		}
	}
	s.mu.Unlock()
	for _, incoming := range expired {
		_ = s.store.DiscardIncoming(incoming)
	}
}

func (s *Service) importLimit(format string) (int64, error) {
	switch format {
	case "txt":
		return s.maxTXTBytes, nil
	case "epub":
		return s.maxEPUBBytes, nil
	default:
		return 0, unsupportedFormat(format)
	}
}

func (s *Service) validateIncoming(format string, incoming store.IncomingFile) error {
	filename, err := s.store.IncomingPath(incoming)
	if err != nil {
		return err
	}
	switch format {
	case "txt":
		if _, err := txttext.ReadFile(filename); err != nil {
			return &ImportError{Code: "unsupported_format", Message: "TXT encoding is not readable"}
		}
		return nil
	case "epub":
		return validateEPUBArchive(filename, s.maxEPUBExpandedBytes)
	default:
		return unsupportedFormat(format)
	}
}

func validateEPUBArchive(filename string, expandedLimit int64) error {
	archive, err := zip.OpenReader(filename)
	if err != nil {
		return &ImportError{Code: "unsupported_format", Message: "file extension is EPUB but content is not a readable ZIP archive"}
	}
	defer archive.Close()
	var expanded uint64
	hasContainer := false
	for _, entry := range archive.File {
		name := strings.ReplaceAll(entry.Name, "\\", "/")
		clean := path.Clean(name)
		if name == "" || path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") {
			return &ImportError{Code: "unsupported_format", Message: fmt.Sprintf("EPUB contains unsafe path %q", entry.Name)}
		}
		if strings.EqualFold(clean, "META-INF/container.xml") {
			hasContainer = true
		}
		if ^uint64(0)-expanded < entry.UncompressedSize64 {
			return &ImportError{Code: "epub_expanded_too_large", Message: "EPUB declared expanded size exceeds 512 MiB"}
		}
		expanded += entry.UncompressedSize64
		if expanded > uint64(expandedLimit) {
			return &ImportError{Code: "epub_expanded_too_large", Message: fmt.Sprintf("EPUB declared expanded size exceeds %d MiB", expandedLimit>>20)}
		}
	}
	if !hasContainer {
		return &ImportError{Code: "unsupported_format", Message: "EPUB is missing META-INF/container.xml"}
	}
	return nil
}

func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || name == "." {
		return "upload"
	}
	var value strings.Builder
	for _, r := range name {
		switch {
		case r == '/' || r == '\\' || r == 0:
			value.WriteRune('_')
		case r < 32:
			value.WriteRune('_')
		default:
			value.WriteRune(r)
		}
	}
	clean := strings.TrimSpace(value.String())
	if clean == "" {
		return "upload"
	}
	return clean
}

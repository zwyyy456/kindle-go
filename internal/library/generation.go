package library

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/store"
)

func (s *Service) ResolveOriginal(ctx context.Context, bookID, fileID, expectedSHA string) (string, File, error) {
	book, ok, err := s.store.Book(ctx, bookID)
	if err != nil {
		return "", File{}, err
	}
	if !ok || book.Original.ID != fileID {
		return "", File{}, wrapSourceUnavailable(os.ErrNotExist)
	}
	path, err := s.store.ResolveRel(book.Original.RelPath)
	if err != nil {
		return "", File{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return "", File{}, wrapSourceUnavailable(err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", File{}, wrapSourceUnavailable(copyErr)
	}
	if closeErr != nil {
		return "", File{}, wrapSourceUnavailable(closeErr)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if size != book.Original.Size || digest != book.Original.SHA256 || (expectedSHA != "" && digest != expectedSHA) {
		return "", File{}, fmt.Errorf("%w: original file changed after import", ErrSourceChanged)
	}
	return path, fileFromStore(book.Original), nil
}

func (s *Service) ResolveInput(ctx context.Context, bookID, fileID, expectedSHA string) (string, File, error) {
	stored, ok, err := s.store.File(ctx, fileID)
	if err != nil {
		return "", File{}, err
	}
	if !ok || stored.BookID != bookID || (stored.Role != "original" && stored.Role != "revision") {
		return "", File{}, wrapSourceUnavailable(os.ErrNotExist)
	}
	path, err := s.store.ResolveRel(stored.RelPath)
	if err != nil {
		return "", File{}, err
	}
	digest, size, err := hashLibraryFile(path)
	if err != nil {
		return "", File{}, wrapSourceUnavailable(err)
	}
	if digest != stored.SHA256 || size != stored.Size || (expectedSHA != "" && digest != expectedSHA) {
		return "", File{}, fmt.Errorf("%w: input file changed after commit", ErrSourceChanged)
	}
	return path, fileFromStore(stored), nil
}

func hashLibraryFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", 0, copyErr
	}
	if closeErr != nil {
		return "", 0, closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func (s *Service) WorkOutputPath(taskID, format string) (string, string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if taskID == "" || (format != "epub" && format != "azw3") {
		return "", "", fmt.Errorf("invalid task work output")
	}
	relPath := filepath.ToSlash(filepath.Join("work", taskID, "output."+format+".part"))
	path, err := s.store.ResolveRel(relPath)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	return path, relPath, nil
}

func (s *Service) CommitGeneratedArtifact(ctx context.Context, req GeneratedArtifact) (File, error) {
	file, err := s.store.CommitArtifact(ctx, store.ArtifactCommit{
		BookID: req.BookID, SourceFileID: req.SourceFileID, TaskID: req.TaskID,
		Format: req.Format, DisplayName: req.DisplayName, WorkRelPath: req.WorkRelPath,
		ParametersJSON: req.ParametersJSON, CreatedAt: req.CreatedAt,
	})
	return fileFromStore(file), err
}

type GeneratedArtifact struct {
	BookID         string
	SourceFileID   string
	TaskID         string
	Format         string
	DisplayName    string
	WorkRelPath    string
	ParametersJSON string
	CreatedAt      time.Time
}

func (s *Service) RemoveTaskWork(taskID string) error {
	if taskID == "" || strings.ContainsAny(taskID, `/\\`) {
		return fmt.Errorf("invalid task ID")
	}
	path, err := s.store.ResolveRel(filepath.ToSlash(filepath.Join("work", taskID)))
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

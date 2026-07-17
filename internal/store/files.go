package store

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
)

type IncomingFile struct {
	ID      string
	RelPath string
	SHA256  string
	Size    int64
}

type ArtifactCommit struct {
	BookID         string
	SourceFileID   string
	TaskID         string
	Format         string
	DisplayName    string
	WorkRelPath    string
	ParametersJSON string
	CreatedAt      time.Time
}

func (s *Store) StageIncoming(reader io.Reader) (IncomingFile, error) {
	id, err := NewID()
	if err != nil {
		return IncomingFile{}, err
	}
	relPath := filepath.ToSlash(filepath.Join("incoming", id+".part"))
	path, err := s.ResolveRel(relPath)
	if err != nil {
		return IncomingFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return IncomingFile{}, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return IncomingFile{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(path)
		if copyErr != nil {
			return IncomingFile{}, copyErr
		}
		return IncomingFile{}, closeErr
	}
	return IncomingFile{ID: id, RelPath: relPath, SHA256: hex.EncodeToString(hash.Sum(nil)), Size: size}, nil
}

func (s *Store) IncomingPath(incoming IncomingFile) (string, error) {
	expected := filepath.ToSlash(filepath.Join("incoming", incoming.ID+".part"))
	if incoming.ID == "" || incoming.RelPath != expected {
		return "", fmt.Errorf("invalid incoming file")
	}
	return s.ResolveRel(incoming.RelPath)
}

func (s *Store) DiscardIncoming(incoming IncomingFile) error {
	path, err := s.IncomingPath(incoming)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Store) CommitIncomingOriginal(ctx context.Context, incoming IncomingFile, displayName, fileName, format string, now time.Time) (Book, error) {
	if now.IsZero() {
		now = time.Now()
	}
	sourcePath, err := s.IncomingPath(incoming)
	if err != nil {
		return Book{}, err
	}
	digest, size, err := hashFile(sourcePath)
	if err != nil {
		return Book{}, err
	}
	if digest != incoming.SHA256 || size != incoming.Size {
		return Book{}, fmt.Errorf("incoming file changed before commit")
	}
	bookID, err := NewID()
	if err != nil {
		return Book{}, err
	}
	fileID, err := NewID()
	if err != nil {
		return Book{}, err
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), "."))
	if ext == "" || ext != format {
		return Book{}, fmt.Errorf("file extension does not match format %q", format)
	}
	relPath := filepath.ToSlash(filepath.Join("originals", bookID, fileID+"."+format))
	finalPath, err := s.ResolveRel(relPath)
	if err != nil {
		return Book{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return Book{}, err
	}
	timestamp := formatTime(now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Book{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO books(id, display_name, source_format, state, imported_at) VALUES(?, ?, ?, 'active', ?)`, bookID, displayName, format, timestamp); err != nil {
		return Book{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, created_at) VALUES(?, ?, 'original', 'pending', ?, ?, ?, ?, ?, ?)`, fileID, bookID, format, fileName, relPath, digest, size, timestamp); err != nil {
		return Book{}, err
	}
	if err := tx.Commit(); err != nil {
		return Book{}, err
	}
	if err := os.Rename(sourcePath, finalPath); err != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, bookID)
		return Book{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET state = 'ready' WHERE id = ? AND state = 'pending'`, fileID); err != nil {
		return Book{}, err
	}
	return Book{
		ID: bookID, DisplayName: displayName, SourceFormat: format, ImportedAt: now,
		Original: File{ID: fileID, BookID: bookID, Role: "original", State: "ready", Format: format, DisplayName: fileName, RelPath: relPath, SHA256: digest, Size: size, CreatedAt: now},
	}, nil
}

func (s *Store) OriginalBooksBySHA(ctx context.Context, digest string) ([]Book, error) {
	return s.listBooks(ctx, `WHERE b.state = 'active' AND o.sha256 = ? ORDER BY b.imported_at DESC, b.id DESC`, digest)
}

func (s *Store) CommitArtifact(ctx context.Context, commit ArtifactCommit) (File, error) {
	workPath, err := s.ResolveRel(commit.WorkRelPath)
	if err != nil {
		return File{}, err
	}
	cleanWork := filepath.ToSlash(filepath.Clean(commit.WorkRelPath))
	if !strings.HasPrefix(cleanWork, "work/"+commit.TaskID+"/") {
		return File{}, fmt.Errorf("artifact work path does not belong to task %q", commit.TaskID)
	}
	digest, size, err := hashFile(workPath)
	if err != nil {
		return File{}, err
	}
	fileID, err := NewID()
	if err != nil {
		return File{}, err
	}
	format := strings.ToLower(strings.TrimSpace(commit.Format))
	if format == "" {
		return File{}, fmt.Errorf("artifact format is required")
	}
	relPath := filepath.ToSlash(filepath.Join("artifacts", commit.BookID, fileID+"."+format))
	finalPath, err := s.ResolveRel(relPath)
	if err != nil {
		return File{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return File{}, err
	}
	createdAt := commit.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	parameters := strings.TrimSpace(commit.ParametersJSON)
	if parameters == "" {
		parameters = "{}"
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, source_file_id, task_id, parameters_json, created_at)
VALUES(?, ?, 'artifact', 'pending', ?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
`, fileID, commit.BookID, format, commit.DisplayName, relPath, digest, size, commit.SourceFileID, commit.TaskID, parameters, formatTime(createdAt))
	if err != nil {
		return File{}, err
	}
	if err := os.Rename(workPath, finalPath); err != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND state = 'pending'`, fileID)
		return File{}, err
	}
	finalizeTx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		_ = os.Remove(finalPath)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM files WHERE id = ? AND state = 'pending'`, fileID)
		return File{}, err
	}
	result, err := finalizeTx.ExecContext(context.Background(), `UPDATE tasks SET status = 'completed', error_code = '', error_message = '', finished_at = ?, stage = 'completed' WHERE id = ? AND status = 'running'`, formatTime(time.Now()), commit.TaskID)
	if err != nil {
		_ = finalizeTx.Rollback()
		_ = os.Remove(finalPath)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM files WHERE id = ? AND state = 'pending'`, fileID)
		return File{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		_ = finalizeTx.Rollback()
		_ = os.Remove(finalPath)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM files WHERE id = ? AND state = 'pending'`, fileID)
		if err != nil {
			return File{}, err
		}
		return File{}, fmt.Errorf("task %q was canceled before artifact commit", commit.TaskID)
	}
	if _, err := finalizeTx.ExecContext(context.Background(), `UPDATE files SET state = 'ready' WHERE id = ? AND state = 'pending'`, fileID); err != nil {
		_ = finalizeTx.Rollback()
		_ = os.Remove(finalPath)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM files WHERE id = ? AND state = 'pending'`, fileID)
		return File{}, err
	}
	if err := finalizeTx.Commit(); err != nil {
		_ = os.Remove(finalPath)
		_, _ = s.db.ExecContext(context.Background(), `DELETE FROM files WHERE id = ? AND state = 'pending'`, fileID)
		return File{}, err
	}
	return File{ID: fileID, BookID: commit.BookID, Role: "artifact", State: "ready", Format: format, DisplayName: commit.DisplayName, RelPath: relPath, SHA256: digest, Size: size, CreatedAt: createdAt}, nil
}

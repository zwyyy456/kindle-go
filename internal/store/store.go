package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const databaseFile = "library.db"

type Store struct {
	root string
	db   *sql.DB
}

type File struct {
	ID          string
	BookID      string
	Role        string
	State       string
	Format      string
	DisplayName string
	RelPath     string
	SHA256      string
	Size        int64
	CreatedAt   time.Time
}

type Book struct {
	ID              string
	DisplayName     string
	SourceFormat    string
	ImportedAt      time.Time
	LegacyLastError string
	Original        File
	LatestArtifact  File
}

func Open(root string) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		root = "kindle-go-library"
	}
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	dbPath, err := filepath.Abs(filepath.Join(root, databaseFile))
	if err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath}).String() + "?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{root: root, db: db}
	ctx := context.Background()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrateLegacyIndex(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.reconcilePendingFiles(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Root() string { return s.root }

func NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (s *Store) ResolveRel(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute library path is not allowed: %s", relPath)
	}
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if cleanRel == "." || cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe library path: %s", relPath)
	}
	rootAbs, err := filepath.Abs(s.root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Join(s.root, cleanRel))
	if err != nil {
		return "", err
	}
	if absPath != rootAbs && !strings.HasPrefix(absPath, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("library path escapes root: %s", relPath)
	}
	return absPath, nil
}

func (s *Store) CreateOriginal(ctx context.Context, displayName, fileName, format string, reader io.Reader, now time.Time) (Book, error) {
	if now.IsZero() {
		now = time.Now()
	}
	bookID, err := NewID()
	if err != nil {
		return Book{}, err
	}
	fileID, err := NewID()
	if err != nil {
		return Book{}, err
	}
	incomingDir := filepath.Join(s.root, "incoming")
	if err := os.MkdirAll(incomingDir, 0o755); err != nil {
		return Book{}, err
	}
	tmpPath := filepath.Join(incomingDir, fileID+".part")
	out, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Book{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(out, hash), reader)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(tmpPath)
		if copyErr != nil {
			return Book{}, copyErr
		}
		return Book{}, closeErr
	}

	relPath := filepath.ToSlash(filepath.Join("originals", bookID, fileID+"."+format))
	finalPath, err := s.ResolveRel(relPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return Book{}, err
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		_ = os.Remove(tmpPath)
		return Book{}, err
	}
	timestamp := formatTime(now)
	digest := hex.EncodeToString(hash.Sum(nil))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		_ = os.Remove(tmpPath)
		return Book{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO books(id, display_name, source_format, state, imported_at) VALUES(?, ?, ?, 'active', ?)`, bookID, displayName, format, timestamp); err != nil {
		tx.Rollback()
		_ = os.Remove(tmpPath)
		return Book{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, created_at) VALUES(?, ?, 'original', 'pending', ?, ?, ?, ?, ?, ?)`, fileID, bookID, format, fileName, relPath, digest, size, timestamp); err != nil {
		tx.Rollback()
		_ = os.Remove(tmpPath)
		return Book{}, err
	}
	if err := tx.Commit(); err != nil {
		_ = os.Remove(tmpPath)
		return Book{}, err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_, _ = s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, bookID)
		_ = os.Remove(tmpPath)
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

func (s *Store) AddArtifact(ctx context.Context, bookID, name, format, relPath string, size int64, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}
	absPath, err := s.ResolveRel(relPath)
	if err != nil {
		return err
	}
	digest, actualSize, err := hashFile(absPath)
	if err != nil {
		return err
	}
	if size != actualSize {
		return fmt.Errorf("artifact size changed: got %d, expected %d", actualSize, size)
	}
	fileID, err := NewID()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, created_at) VALUES(?, ?, 'artifact', 'ready', ?, ?, ?, ?, ?, ?)`, fileID, bookID, format, name, filepath.ToSlash(relPath), digest, size, formatTime(now))
	if err == nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE books SET legacy_last_error = '' WHERE id = ?`, bookID)
	}
	return err
}

func (s *Store) SetLegacyError(ctx context.Context, bookID string, value string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE books SET legacy_last_error = ? WHERE id = ?`, value, bookID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("book %q not found", bookID)
	}
	return nil
}

func (s *Store) Book(ctx context.Context, id string) (Book, bool, error) {
	books, err := s.listBooks(ctx, `WHERE b.id = ?`, id)
	if err != nil {
		return Book{}, false, err
	}
	if len(books) == 0 {
		return Book{}, false, nil
	}
	return books[0], true, nil
}

func (s *Store) AllBooks(ctx context.Context) ([]Book, error) {
	return s.listBooks(ctx, `WHERE b.state = 'active' ORDER BY b.imported_at DESC, b.id DESC`)
}

func (s *Store) RecentBooks(ctx context.Context, cutoff time.Time) ([]Book, error) {
	return s.listBooks(ctx, `WHERE b.state = 'active' AND b.imported_at >= ? ORDER BY b.imported_at DESC, b.id DESC`, formatTime(cutoff))
}

func (s *Store) listBooks(ctx context.Context, where string, args ...any) ([]Book, error) {
	query := `
SELECT b.id, b.display_name, b.source_format, b.imported_at, b.legacy_last_error,
       o.id, o.state, o.format, o.display_name, o.rel_path, o.sha256, o.size_bytes, o.created_at,
       COALESCE(a.id, ''), COALESCE(a.state, ''), COALESCE(a.format, ''), COALESCE(a.display_name, ''),
       COALESCE(a.rel_path, ''), COALESCE(a.sha256, ''), COALESCE(a.size_bytes, 0), COALESCE(a.created_at, '')
FROM books b
JOIN files o ON o.book_id = b.id AND o.role = 'original' AND o.state = 'ready'
LEFT JOIN files a ON a.id = (
    SELECT f.id FROM files f
    WHERE f.book_id = b.id AND f.role = 'artifact' AND f.state = 'ready'
    ORDER BY f.created_at DESC, f.id DESC LIMIT 1
)
` + where
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var books []Book
	for rows.Next() {
		var book Book
		var imported, originalCreated, artifactCreated string
		if err := rows.Scan(
			&book.ID, &book.DisplayName, &book.SourceFormat, &imported, &book.LegacyLastError,
			&book.Original.ID, &book.Original.State, &book.Original.Format, &book.Original.DisplayName, &book.Original.RelPath, &book.Original.SHA256, &book.Original.Size, &originalCreated,
			&book.LatestArtifact.ID, &book.LatestArtifact.State, &book.LatestArtifact.Format, &book.LatestArtifact.DisplayName, &book.LatestArtifact.RelPath, &book.LatestArtifact.SHA256, &book.LatestArtifact.Size, &artifactCreated,
		); err != nil {
			return nil, err
		}
		book.ImportedAt, err = parseTime(imported)
		if err != nil {
			return nil, err
		}
		book.Original.BookID = book.ID
		book.Original.Role = "original"
		book.Original.CreatedAt, err = parseTime(originalCreated)
		if err != nil {
			return nil, err
		}
		if book.LatestArtifact.ID != "" {
			book.LatestArtifact.BookID = book.ID
			book.LatestArtifact.Role = "artifact"
			book.LatestArtifact.CreatedAt, err = parseTime(artifactCreated)
			if err != nil {
				return nil, err
			}
		}
		books = append(books, book)
	}
	return books, rows.Err()
}

func hashFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func formatTime(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored time %q: %w", value, err)
	}
	return parsed, nil
}

func (s *Store) reconcilePendingFiles(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, book_id, role, rel_path, sha256, size_bytes FROM files WHERE state = 'pending'`)
	if err != nil {
		return err
	}
	type pendingFile struct {
		id, bookID, role, relPath, sha256 string
		size                              int64
	}
	var pending []pendingFile
	for rows.Next() {
		var file pendingFile
		if err := rows.Scan(&file.id, &file.bookID, &file.role, &file.relPath, &file.sha256, &file.size); err != nil {
			rows.Close()
			return err
		}
		pending = append(pending, file)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, file := range pending {
		path, err := s.ResolveRel(file.relPath)
		if err == nil {
			var digest string
			var size int64
			digest, size, err = hashFile(path)
			if err == nil && digest == file.sha256 && size == file.size {
				if _, err := s.db.ExecContext(ctx, `UPDATE files SET state = 'ready' WHERE id = ? AND state = 'pending'`, file.id); err != nil {
					return err
				}
				continue
			}
		}
		if path != "" {
			_ = os.Remove(path)
		}
		if file.role == "original" {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ?`, file.bookID); err != nil {
				return err
			}
		} else if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, file.id); err != nil {
			return err
		}
	}
	return nil
}

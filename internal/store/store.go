package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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
	ID             string
	BookID         string
	Role           string
	State          string
	Format         string
	DisplayName    string
	RelPath        string
	SHA256         string
	Size           int64
	SourceFileID   string
	TaskID         string
	ProofreadRunID string
	ParametersJSON string
	HasUnresolved  bool
	CreatedAt      time.Time
}

func (s *Store) File(ctx context.Context, id string) (File, bool, error) {
	file, err := scanFile(s.db.QueryRowContext(ctx, fileSelect+` WHERE id = ? AND state = 'ready'`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, false, nil
	}
	return file, err == nil, err
}

func (s *Store) FilesForBook(ctx context.Context, bookID string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, fileSelect+` WHERE book_id = ? AND state = 'ready' ORDER BY created_at DESC, id DESC`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var files []File
	for rows.Next() {
		file, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

const fileSelect = `SELECT id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, COALESCE(source_file_id, ''), COALESCE(task_id, ''), COALESCE(proofread_run_id, ''), COALESCE(parameters_json, ''), has_unresolved, created_at FROM files`

func scanFile(row rowScanner) (File, error) {
	var file File
	var unresolved int
	var created string
	if err := row.Scan(&file.ID, &file.BookID, &file.Role, &file.State, &file.Format, &file.DisplayName, &file.RelPath, &file.SHA256, &file.Size, &file.SourceFileID, &file.TaskID, &file.ProofreadRunID, &file.ParametersJSON, &unresolved, &created); err != nil {
		return File{}, err
	}
	file.HasUnresolved = unresolved != 0
	var err error
	file.CreatedAt, err = parseTime(created)
	return file, err
}

type Book struct {
	ID              string
	DisplayName     string
	SourceFormat    string
	ProofreadStatus string
	ImportedAt      time.Time
	LegacyLastError string
	Original        File
	LatestArtifact  File
}

func Open(root string) (*Store, error) {
	s, err := open(root)
	if err != nil {
		return nil, err
	}
	if err := s.Initialize(context.Background()); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// OpenForWorker opens the schema but defers library reconciliation until the
// task runner has acquired the runtime lock.
func OpenForWorker(root string) (*Store, error) {
	return open(root)
}

func open(root string) (*Store, error) {
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
	return s, nil
}

func (s *Store) Initialize(ctx context.Context) error {
	if err := s.migrateLegacyIndex(ctx); err != nil {
		return err
	}
	if err := s.reconcilePendingFiles(ctx); err != nil {
		return err
	}
	if err := s.reconcileDeletingBooks(ctx); err != nil {
		return err
	}
	if err := s.cleanupIncoming(); err != nil {
		return err
	}
	return s.cleanupWork()
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
	incoming, err := s.StageIncoming(reader)
	if err != nil {
		return Book{}, err
	}
	book, err := s.CommitIncomingOriginal(ctx, incoming, displayName, fileName, format, now)
	if err != nil {
		_ = s.DiscardIncoming(incoming)
	}
	return book, err
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

func (s *Store) BooksPage(ctx context.Context, search, sortOrder, statusFilter string, limit, offset int) ([]Book, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	where := `WHERE b.state = 'active'`
	var args []any
	if strings.TrimSpace(search) != "" {
		where += ` AND b.display_name LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(strings.TrimSpace(search))+"%")
	}
	if statusFilter != "" {
		where += ` AND ` + proofreadStatusSQL + ` = ?`
		args = append(args, statusFilter)
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books b `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := ` ORDER BY b.imported_at DESC, b.id DESC`
	switch sortOrder {
	case "name_asc":
		order = ` ORDER BY b.display_name COLLATE NOCASE ASC, b.id ASC`
	case "imported_asc":
		order = ` ORDER BY b.imported_at ASC, b.id ASC`
	case "status_asc":
		order = ` ORDER BY ` + proofreadStatusSQL + ` ASC, b.imported_at DESC, b.id DESC`
	}
	queryArgs := append(append([]any(nil), args...), limit, offset)
	books, err := s.listBooks(ctx, where+order+` LIMIT ? OFFSET ?`, queryArgs...)
	return books, total, err
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (s *Store) listBooks(ctx context.Context, where string, args ...any) ([]Book, error) {
	query := `
SELECT b.id, b.display_name, b.source_format, b.imported_at, b.legacy_last_error,
       ` + proofreadStatusSQL + `,
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
			&book.ID, &book.DisplayName, &book.SourceFormat, &imported, &book.LegacyLastError, &book.ProofreadStatus,
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

const proofreadStatusSQL = `COALESCE((
    SELECT t.status FROM tasks t
    WHERE t.book_id = b.id AND t.type = 'proofread'
    ORDER BY t.queue_seq DESC LIMIT 1
), 'not_started')`

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
		if file.role == "original" && err == nil {
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

func (s *Store) cleanupIncoming() error {
	directory := filepath.Join(s.root, "incoming")
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".part") {
			if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

func (s *Store) cleanupWork() error {
	directory, err := s.ResolveRel("work")
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(directory)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(directory, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

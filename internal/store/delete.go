package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ActiveTasksError struct {
	BookID string
	Count  int
}

type ReferencedFileError struct {
	FileID string
	Count  int
}

type bookDeletionPaths struct {
	files           []string
	proofreadStates []string
}

func (e *ActiveTasksError) Error() string {
	return fmt.Sprintf("book_has_active_tasks: book %s has %d queued or running task(s)", e.BookID, e.Count)
}

func (e *ReferencedFileError) Error() string {
	return fmt.Sprintf("file_has_references: file %s is referenced by %d ready or pending file(s)", e.FileID, e.Count)
}

func (s *Store) DeleteBook(ctx context.Context, bookID string) error {
	paths, err := s.beginBookDeletion(ctx, bookID)
	if err != nil {
		return err
	}
	if err := s.removeBookResources(bookID, paths); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ? AND state = 'deleting'`, bookID)
	return err
}

func (s *Store) DeleteArtifact(ctx context.Context, fileID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var references int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM files WHERE source_file_id = ? AND state IN ('ready', 'pending')`, fileID).Scan(&references); err != nil {
		return err
	}
	if references != 0 {
		return &ReferencedFileError{FileID: fileID, Count: references}
	}
	var role, relPath string
	if err := tx.QueryRowContext(ctx, `SELECT role, rel_path FROM files WHERE id = ? AND state = 'ready'`, fileID).Scan(&role, &relPath); errors.Is(err, sql.ErrNoRows) {
		return os.ErrNotExist
	} else if err != nil {
		return err
	}
	if role != "artifact" && role != "revision" && role != "report" && role != "audit" {
		return fmt.Errorf("original files cannot be deleted separately")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE files SET state = 'deleting' WHERE id = ? AND state = 'ready'`, fileID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.removeLibraryFiles([]string{relPath}); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND state = 'deleting'`, fileID)
	return err
}

func (s *Store) beginBookDeletion(ctx context.Context, bookID string) (bookDeletionPaths, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return bookDeletionPaths{}, err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM books WHERE id = ?`, bookID).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return bookDeletionPaths{}, os.ErrNotExist
	} else if err != nil {
		return bookDeletionPaths{}, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE book_id = ? AND status IN ('queued', 'running')`, bookID).Scan(&active); err != nil {
		return bookDeletionPaths{}, err
	}
	if active != 0 {
		return bookDeletionPaths{}, &ActiveTasksError{BookID: bookID, Count: active}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE books SET state = 'deleting' WHERE id = ?`, bookID); err != nil {
		return bookDeletionPaths{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE files SET state = 'deleting' WHERE book_id = ?`, bookID); err != nil {
		return bookDeletionPaths{}, err
	}
	paths, err := collectBookDeletionPaths(ctx, tx, bookID)
	if err != nil {
		return bookDeletionPaths{}, err
	}
	if err := tx.Commit(); err != nil {
		return bookDeletionPaths{}, err
	}
	return paths, nil
}

func (s *Store) reconcileDeletingBooks(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM books WHERE state = 'deleting' ORDER BY id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		paths, err := collectBookDeletionPaths(ctx, s.db, id)
		if err != nil {
			return err
		}
		if err := s.removeBookResources(id, paths); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM books WHERE id = ? AND state = 'deleting'`, id); err != nil {
			return err
		}
	}
	return s.reconcileDeletingFiles(ctx)
}

func (s *Store) reconcileDeletingFiles(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.rel_path FROM files f JOIN books b ON b.id = f.book_id WHERE f.state = 'deleting' AND b.state = 'active'`)
	if err != nil {
		return err
	}
	type deletingFile struct{ id, relPath string }
	var files []deletingFile
	for rows.Next() {
		var file deletingFile
		if err := rows.Scan(&file.id, &file.relPath); err != nil {
			rows.Close()
			return err
		}
		files = append(files, file)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, file := range files {
		if err := s.removeLibraryFiles([]string{file.relPath}); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ? AND state = 'deleting'`, file.id); err != nil {
			return err
		}
	}
	return nil
}

type deletionPathQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func collectBookDeletionPaths(ctx context.Context, query deletionPathQuerier, bookID string) (bookDeletionPaths, error) {
	var paths bookDeletionPaths
	rows, err := query.QueryContext(ctx, `
SELECT rel_path, 'file' FROM files WHERE book_id = ?
UNION ALL
SELECT engine_state_rel_path, 'proofread_state' FROM proofread_runs WHERE book_id = ? AND engine_state_rel_path <> ''
`, bookID, bookID)
	if err != nil {
		return bookDeletionPaths{}, err
	}
	for rows.Next() {
		var relPath, kind string
		if err := rows.Scan(&relPath, &kind); err != nil {
			rows.Close()
			return bookDeletionPaths{}, err
		}
		if kind == "proofread_state" {
			paths.proofreadStates = append(paths.proofreadStates, relPath)
		} else {
			paths.files = append(paths.files, relPath)
		}
	}
	if err := rows.Close(); err != nil {
		return bookDeletionPaths{}, err
	}
	return paths, nil
}

func (s *Store) removeBookResources(bookID string, paths bookDeletionPaths) error {
	if err := s.removeLibraryFiles(paths.files); err != nil {
		return err
	}
	for _, relPath := range paths.proofreadStates {
		if !validProofreadStatePath(bookID, relPath) {
			return fmt.Errorf("invalid proofread state path %q for book %q", relPath, bookID)
		}
		path, err := s.ResolveRel(relPath)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		removeEmptyParents(filepath.Dir(path), s.root)
	}
	return nil
}

func validProofreadStatePath(bookID, relPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(relPath))
	parts := strings.Split(clean, "/")
	return clean == relPath && len(parts) == 4 &&
		parts[0] == "proofreads" && parts[1] == bookID && parts[2] != "" && parts[3] == "state"
}

func (s *Store) removeLibraryFiles(relPaths []string) error {
	for _, relPath := range relPaths {
		path, err := s.ResolveRel(relPath)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		removeEmptyParents(filepath.Dir(path), s.root)
	}
	return nil
}

func removeEmptyParents(directory, root string) {
	root, err := filepath.Abs(root)
	if err != nil {
		return
	}
	for {
		directory, err = filepath.Abs(directory)
		if err != nil || directory == root || len(directory) <= len(root) {
			return
		}
		if err := os.Remove(directory); err != nil {
			return
		}
		directory = filepath.Dir(directory)
	}
}

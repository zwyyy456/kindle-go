package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type ActiveTasksError struct {
	BookID string
	Count  int
}

func (e *ActiveTasksError) Error() string {
	return fmt.Sprintf("book_has_active_tasks: book %s has %d queued or running task(s)", e.BookID, e.Count)
}

func (s *Store) DeleteBook(ctx context.Context, bookID string) error {
	relPaths, err := s.beginBookDeletion(ctx, bookID)
	if err != nil {
		return err
	}
	if err := s.removeLibraryFiles(relPaths); err != nil {
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

func (s *Store) beginBookDeletion(ctx context.Context, bookID string) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM books WHERE id = ?`, bookID).Scan(&state); errors.Is(err, sql.ErrNoRows) {
		return nil, os.ErrNotExist
	} else if err != nil {
		return nil, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE book_id = ? AND status IN ('queued', 'running')`, bookID).Scan(&active); err != nil {
		return nil, err
	}
	if active != 0 {
		return nil, &ActiveTasksError{BookID: bookID, Count: active}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE books SET state = 'deleting' WHERE id = ?`, bookID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE files SET state = 'deleting' WHERE book_id = ?`, bookID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT rel_path FROM files WHERE book_id = ?`, bookID)
	if err != nil {
		return nil, err
	}
	var relPaths []string
	for rows.Next() {
		var relPath string
		if err := rows.Scan(&relPath); err != nil {
			rows.Close()
			return nil, err
		}
		relPaths = append(relPaths, relPath)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return relPaths, nil
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
		fileRows, err := s.db.QueryContext(ctx, `SELECT rel_path FROM files WHERE book_id = ?`, id)
		if err != nil {
			return err
		}
		var relPaths []string
		for fileRows.Next() {
			var relPath string
			if err := fileRows.Scan(&relPath); err != nil {
				fileRows.Close()
				return err
			}
			relPaths = append(relPaths, relPath)
		}
		if err := fileRows.Close(); err != nil {
			return err
		}
		if err := s.removeLibraryFiles(relPaths); err != nil {
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

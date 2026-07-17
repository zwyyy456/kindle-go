package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type legacyIndex struct {
	Records []legacyRecord `json:"records"`
}

type legacyRecord struct {
	ID           string     `json:"id"`
	OriginalName string     `json:"original_name"`
	UploadedAt   time.Time  `json:"uploaded_at"`
	Original     legacyFile `json:"original"`
	Output       legacyFile `json:"output"`
	LastError    string     `json:"last_error"`
}

type legacyFile struct {
	Name      string    `json:"name"`
	RelPath   string    `json:"rel_path"`
	Format    string    `json:"format"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) migrateLegacyIndex(ctx context.Context) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM books`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root, "index.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil
	}
	var index legacyIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("read legacy library index: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, record := range index.Records {
		if strings.TrimSpace(record.ID) == "" {
			return fmt.Errorf("legacy record has empty id")
		}
		original, err := s.inspectLegacyFile(record.Original)
		if err != nil {
			return fmt.Errorf("migrate legacy record %q original: %w", record.ID, err)
		}
		imported := record.UploadedAt
		if imported.IsZero() {
			imported = original.CreatedAt
		}
		if imported.IsZero() {
			imported = time.Now()
		}
		format := strings.ToLower(strings.TrimSpace(original.Format))
		if format == "" {
			format = strings.ToLower(strings.TrimPrefix(filepath.Ext(original.Name), "."))
		}
		if format == "" {
			format = "file"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO books(id, display_name, source_format, state, imported_at, legacy_last_error) VALUES(?, ?, ?, 'active', ?, ?)`, record.ID, record.OriginalName, format, formatTime(imported), record.LastError); err != nil {
			return err
		}
		fileID, err := NewID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, created_at) VALUES(?, ?, 'original', 'ready', ?, ?, ?, ?, ?, ?)`, fileID, record.ID, format, original.Name, original.RelPath, original.SHA256, original.Size, formatTime(original.CreatedAt)); err != nil {
			return err
		}
		if strings.TrimSpace(record.Output.RelPath) != "" {
			output, err := s.inspectLegacyFile(record.Output)
			if err != nil {
				return fmt.Errorf("migrate legacy record %q output: %w", record.ID, err)
			}
			outputID, err := NewID()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO files(id, book_id, role, state, format, display_name, rel_path, sha256, size_bytes, parameters_json, created_at) VALUES(?, ?, 'artifact', 'ready', ?, ?, ?, ?, ?, '{"legacy_import":true}', ?)`, outputID, record.ID, strings.ToLower(output.Format), output.Name, output.RelPath, output.SHA256, output.Size, formatTime(output.CreatedAt)); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value_json, updated_at) VALUES('legacy.index_migrated', 'true', ?) ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`, formatTime(time.Now())); err != nil {
		return err
	}
	return tx.Commit()
}

type inspectedLegacyFile struct {
	legacyFile
	SHA256 string
}

func (s *Store) inspectLegacyFile(file legacyFile) (inspectedLegacyFile, error) {
	path, err := s.ResolveRel(file.RelPath)
	if err != nil {
		return inspectedLegacyFile{}, err
	}
	handle, err := os.Open(path)
	if err != nil {
		return inspectedLegacyFile{}, err
	}
	defer handle.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, handle)
	if err != nil {
		return inspectedLegacyFile{}, err
	}
	created := file.CreatedAt
	if created.IsZero() {
		if info, statErr := handle.Stat(); statErr == nil {
			created = info.ModTime()
		}
	}
	return inspectedLegacyFile{legacyFile: legacyFile{Name: file.Name, RelPath: filepath.ToSlash(file.RelPath), Format: file.Format, Size: size, CreatedAt: created}, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	appstore "github.com/flashdict/kindle2flashdict/internal/store"
)

type Library struct {
	root  string
	store *appstore.Store
}

type Record struct {
	ID           string
	OriginalName string
	UploadedAt   time.Time
	Original     FileEntry
	Output       FileEntry
	LastError    string
}

type FileEntry struct {
	Name      string
	RelPath   string
	Format    string
	Size      int64
	CreatedAt time.Time
}

func NewLibrary(root string) (*Library, error) {
	store, err := appstore.Open(root)
	if err != nil {
		return nil, err
	}
	return &Library{root: store.Root(), store: store}, nil
}

func (l *Library) Close() error { return l.store.Close() }

func (l *Library) Root() string { return l.root }

func (l *Library) AddUpload(name string, reader io.Reader, now time.Time) (Record, error) {
	if strings.TrimSpace(name) == "" {
		name = "upload"
	}
	cleanName := safeFileName(filepath.Base(name))
	format := formatOf(cleanName)
	if format != "txt" && format != "epub" {
		return Record{}, fmt.Errorf("unsupported upload format %q", format)
	}
	book, err := l.store.CreateOriginal(context.Background(), name, cleanName, format, reader, now)
	if err != nil {
		return Record{}, err
	}
	return recordFromStore(book), nil
}

func (l *Library) AddConverted(recordID, name, relPath string, size int64, now time.Time) error {
	return l.store.AddArtifact(context.Background(), recordID, safeFileName(name), formatOf(name), relPath, size, now)
}

func (l *Library) SetError(recordID string, value error) error {
	message := ""
	if value != nil {
		message = value.Error()
	}
	return l.store.SetLegacyError(context.Background(), recordID, message)
}

func (l *Library) Record(id string) (Record, bool) {
	book, ok, err := l.store.Book(context.Background(), id)
	if err != nil || !ok {
		return Record{}, false
	}
	return recordFromStore(book), true
}

func (l *Library) Recent(now time.Time, window time.Duration) []Record {
	if now.IsZero() {
		now = time.Now()
	}
	books, err := l.store.RecentBooks(context.Background(), now.Add(-window))
	if err != nil {
		return nil
	}
	return recordsFromStore(books)
}

func (l *Library) All() []Record {
	books, err := l.store.AllBooks(context.Background())
	if err != nil {
		return nil
	}
	return recordsFromStore(books)
}

func (l *Library) ResolveFile(recordID, kind string) (string, FileEntry, error) {
	record, ok := l.Record(recordID)
	if !ok {
		return "", FileEntry{}, os.ErrNotExist
	}
	var file FileEntry
	switch kind {
	case "original":
		file = record.Original
	case "output":
		file = record.Output
	default:
		return "", FileEntry{}, os.ErrNotExist
	}
	if file.RelPath == "" {
		return "", FileEntry{}, os.ErrNotExist
	}
	path, err := l.resolveRel(file.RelPath)
	return path, file, err
}

func (l *Library) OriginalPath(record Record) (string, FileEntry, error) {
	if record.Original.RelPath == "" {
		return "", FileEntry{}, errors.New("record has no original file")
	}
	path, err := l.resolveRel(record.Original.RelPath)
	return path, record.Original, err
}

func (l *Library) ConvertedPath(recordID, outputName string) (string, string, error) {
	fileID, err := appstore.NewID()
	if err != nil {
		return "", "", err
	}
	cleanName := safeFileName(outputName)
	relPath := filepath.Join("artifacts", recordID, fileID+filepath.Ext(cleanName))
	absPath, err := l.resolveRel(relPath)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", "", err
	}
	return absPath, filepath.ToSlash(relPath), nil
}

func (l *Library) resolveRel(relPath string) (string, error) { return l.store.ResolveRel(relPath) }

func recordsFromStore(books []appstore.Book) []Record {
	records := make([]Record, 0, len(books))
	for _, book := range books {
		records = append(records, recordFromStore(book))
	}
	sortRecords(records)
	return records
}

func recordFromStore(book appstore.Book) Record {
	return Record{
		ID:           book.ID,
		OriginalName: book.DisplayName,
		UploadedAt:   book.ImportedAt,
		Original:     fileFromStore(book.Original),
		Output:       fileFromStore(book.LatestArtifact),
		LastError:    book.LegacyLastError,
	}
}

func fileFromStore(file appstore.File) FileEntry {
	return FileEntry{Name: file.DisplayName, RelPath: file.RelPath, Format: file.Format, Size: file.Size, CreatedAt: file.CreatedAt}
}

func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool { return records[i].UploadedAt.After(records[j].UploadedAt) })
}

func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '.' || r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._-")
	if out == "" {
		return "file"
	}
	return out
}

func formatOf(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	if ext == "" {
		return "file"
	}
	return ext
}

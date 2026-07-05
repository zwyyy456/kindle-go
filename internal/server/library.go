package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	originalsDir = "originals"
	convertedDir = "converted"
	indexFile    = "index.json"
)

type Library struct {
	root  string
	mu    sync.Mutex
	index Index
}

type Index struct {
	Records []Record `json:"records"`
}

type Record struct {
	ID           string    `json:"id"`
	OriginalName string    `json:"original_name"`
	UploadedAt   time.Time `json:"uploaded_at"`
	Original     FileEntry `json:"original"`
	Output       FileEntry `json:"output,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type FileEntry struct {
	Name      string    `json:"name"`
	RelPath   string    `json:"rel_path"`
	Format    string    `json:"format"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

func NewLibrary(root string) (*Library, error) {
	if root == "" {
		root = "kindle-go-library"
	}
	root = filepath.Clean(root)
	for _, dir := range []string{root, filepath.Join(root, originalsDir), filepath.Join(root, convertedDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	l := &Library{root: root}
	if err := l.load(); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *Library) Root() string {
	return l.root
}

func (l *Library) AddUpload(name string, r io.Reader, now time.Time) (Record, error) {
	if strings.TrimSpace(name) == "" {
		name = "upload"
	}
	if now.IsZero() {
		now = time.Now()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	recordID, err := randomID()
	if err != nil {
		return Record{}, err
	}

	cleanName := safeFileName(filepath.Base(name))
	relPath := filepath.Join(originalsDir, recordID+"-"+cleanName)
	absPath, err := l.resolveRel(relPath)
	if err != nil {
		return Record{}, err
	}
	out, err := os.OpenFile(absPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return Record{}, err
	}
	size, copyErr := io.Copy(out, r)
	closeErr := out.Close()
	if copyErr != nil {
		return Record{}, copyErr
	}
	if closeErr != nil {
		return Record{}, closeErr
	}

	record := Record{
		ID:           recordID,
		OriginalName: name,
		UploadedAt:   now,
		Original: FileEntry{
			Name:      cleanName,
			RelPath:   filepath.ToSlash(relPath),
			Format:    formatOf(cleanName),
			Size:      size,
			CreatedAt: now,
		},
	}
	l.index.Records = append(l.index.Records, record)
	if err := l.saveLocked(); err != nil {
		return Record{}, err
	}
	return record, nil
}

func (l *Library) AddConverted(recordID, name, relPath string, size int64, now time.Time) error {
	if now.IsZero() {
		now = time.Now()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	record, ok := l.recordByIDLocked(recordID)
	if !ok {
		return fmt.Errorf("record %q not found", recordID)
	}
	record.Output = FileEntry{
		Name:      safeFileName(name),
		RelPath:   filepath.ToSlash(relPath),
		Format:    formatOf(name),
		Size:      size,
		CreatedAt: now,
	}
	record.LastError = ""
	return l.saveLocked()
}

func (l *Library) SetError(recordID string, err error) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	record, ok := l.recordByIDLocked(recordID)
	if !ok {
		return fmt.Errorf("record %q not found", recordID)
	}
	if err == nil {
		record.LastError = ""
	} else {
		record.LastError = err.Error()
	}
	return l.saveLocked()
}

func (l *Library) Record(id string) (Record, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	record, ok := l.recordByIDLocked(id)
	if !ok {
		return Record{}, false
	}
	return *record, true
}

func (l *Library) Recent(now time.Time, window time.Duration) []Record {
	if now.IsZero() {
		now = time.Now()
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-window)
	var records []Record
	for _, record := range l.index.Records {
		if !record.UploadedAt.Before(cutoff) {
			records = append(records, record)
		}
	}
	sortRecords(records)
	return records
}

func (l *Library) All() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()

	records := make([]Record, 0, len(l.index.Records))
	for _, record := range l.index.Records {
		records = append(records, record)
	}
	sortRecords(records)
	return records
}

func (l *Library) ResolveFile(recordID, kind string) (string, FileEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	record, ok := l.recordByIDLocked(recordID)
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
	cleanName := safeFileName(outputName)
	relPath := filepath.Join(convertedDir, recordID+"-"+cleanName)
	absPath, err := l.resolveRel(relPath)
	if err != nil {
		return "", "", err
	}
	return absPath, filepath.ToSlash(relPath), nil
}

func (l *Library) load() error {
	path := filepath.Join(l.root, indexFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		l.index = Index{}
		return nil
	}
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		l.index = Index{}
		return nil
	}
	return json.Unmarshal(data, &l.index)
}

func (l *Library) saveLocked() error {
	data, err := json.MarshalIndent(l.index, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(l.root, indexFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(l.root, indexFile))
}

func (l *Library) resolveRel(relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("absolute library path is not allowed: %s", relPath)
	}
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if cleanRel == "." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || cleanRel == ".." {
		return "", fmt.Errorf("unsafe library path: %s", relPath)
	}
	rootAbs, err := filepath.Abs(l.root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(filepath.Join(l.root, cleanRel))
	if err != nil {
		return "", err
	}
	if absPath != rootAbs && !strings.HasPrefix(absPath, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("library path escapes root: %s", relPath)
	}
	return absPath, nil
}

func (l *Library) recordByIDLocked(id string) (*Record, bool) {
	for i := range l.index.Records {
		if l.index.Records[i].ID == id {
			return &l.index.Records[i], true
		}
	}
	return nil, false
}

func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].UploadedAt.After(records[j].UploadedAt)
	})
}

func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
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

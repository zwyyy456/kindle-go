package azw3

import (
	"os"
	"path/filepath"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type Options struct {
	SVGConverter string
}

func Write(path string, b ebook.Book, opts Options) error {
	_ = opts
	normalized := normalizeBook(b)
	compiled, err := compileBook(normalized)
	if err != nil {
		return err
	}
	records, err := buildRecords(compiled)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return writePalmDB(file, compiled.metadata.Title, records)
}

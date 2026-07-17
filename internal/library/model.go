package library

import (
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	MaxTXTBytes          int64 = 32 << 20
	MaxEPUBBytes         int64 = 64 << 20
	MaxEPUBExpandedBytes int64 = 512 << 20
)

type File struct {
	ID             string
	BookID         string
	Role           string
	Format         string
	DisplayName    string
	SHA256         string
	Size           int64
	SourceFileID   string
	TaskID         string
	ParametersJSON string
	HasUnresolved  bool
	CreatedAt      time.Time
}

type BookDetail struct {
	Book  Book
	Files []File
}

type KindleBook struct {
	Book Book
	File File
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

type ImportRequest struct {
	Filename string
	Reader   io.Reader
	Now      time.Time
}

type ImportResult struct {
	Book           Book
	Duplicate      bool
	DuplicateToken string
	Existing       []Book
	ExpiresAt      time.Time
}

type PendingDuplicate struct {
	Token     string
	Filename  string
	Existing  []Book
	ExpiresAt time.Time
}

type ImportError struct {
	Code    string
	Message string
}

func (e *ImportError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func ErrorCode(err error) string {
	var importErr *ImportError
	if errors.As(err, &importErr) {
		return importErr.Code
	}
	return ""
}

type DuplicateTokenError struct {
	Message string
}

func (e *DuplicateTokenError) Error() string {
	if e.Message == "" {
		return "duplicate import token is invalid or expired"
	}
	return e.Message
}

func unsupportedFormat(format string) error {
	return &ImportError{Code: "unsupported_format", Message: fmt.Sprintf("only TXT and EPUB files can be imported (got %q)", format)}
}

package epub

import (
	"archive/zip"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
)

type archive struct {
	closer       *zip.ReadCloser
	files        map[string]*zip.File
	invalidPaths []string
}

func openArchive(filename string) (*archive, error) {
	zr, err := zip.OpenReader(filename)
	if err != nil {
		return nil, fmt.Errorf("open epub: %w", err)
	}
	a := &archive{closer: zr, files: make(map[string]*zip.File, len(zr.File))}
	for _, file := range zr.File {
		name, err := cleanArchivePath(file.Name)
		if err != nil {
			a.invalidPaths = append(a.invalidPaths, file.Name)
			continue
		}
		if strings.HasSuffix(file.Name, "/") {
			continue
		}
		a.files[name] = file
	}
	return a, nil
}

func (a *archive) Close() error { return a.closer.Close() }

func (a *archive) read(name string) ([]byte, error) {
	clean, err := cleanArchivePath(name)
	if err != nil {
		return nil, err
	}
	file := a.files[clean]
	if file == nil {
		return nil, fmt.Errorf("epub entry %q not found", clean)
	}
	r, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("open epub entry %q: %w", clean, err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read epub entry %q: %w", clean, err)
	}
	return data, nil
}

func (a *archive) size(name string) (uint64, error) {
	clean, err := cleanArchivePath(name)
	if err != nil {
		return 0, err
	}
	file := a.files[clean]
	if file == nil {
		return 0, fmt.Errorf("epub entry %q not found", clean)
	}
	return file.UncompressedSize64, nil
}

func cleanArchivePath(name string) (string, error) {
	name = strings.ReplaceAll(strings.TrimSpace(name), `\`, "/")
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("absolute epub path %q", name)
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("epub path escapes archive root: %q", name)
	}
	return clean, nil
}

func resolveReference(baseFile, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty epub reference")
	}
	u, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parse epub reference %q: %w", ref, err)
	}
	if u.Scheme != "" || u.Host != "" {
		return ref, nil
	}
	decoded, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", fmt.Errorf("decode epub reference %q: %w", ref, err)
	}
	if decoded == "" {
		decoded = path.Base(baseFile)
	}
	joined, err := cleanArchivePath(path.Join(path.Dir(baseFile), decoded))
	if err != nil {
		return "", err
	}
	if u.Fragment != "" {
		joined += "#" + u.Fragment
	}
	return joined, nil
}

func referencePath(ref string) string {
	base, _, _ := strings.Cut(ref, "#")
	return base
}

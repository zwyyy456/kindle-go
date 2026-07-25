package cmd

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPreviewPrintsTOCWithoutWritingOutput(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.txt")
	output := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(input, []byte("第一章 开始\n正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--preview", "-o", output, input}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v, stderr=%s", err, stderr.String())
	}
	if text := stdout.String(); !strings.Contains(text, "toc:") || !strings.Contains(text, "第一章 开始") {
		t.Fatalf("preview output = %q", text)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("preview output file exists or stat failed unexpectedly: %v", err)
	}
}

func TestRunVerbosePrintsDetailedTXTAnalysis(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.txt")
	output := filepath.Join(dir, "book.epub")
	if err := os.WriteFile(input, []byte("第一章 开始\n正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--verbose", "-o", output, input}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v, stderr=%s", err, stderr.String())
	}
	if text := stdout.String(); !strings.Contains(text, "charset:") || !strings.Contains(text, "toc:") {
		t.Fatalf("verbose output = %q", text)
	}
}

func TestRunConvertsEPUBToAZW3(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.epub")
	writeCommandEPUBFixture(t, input)
	output := filepath.Join(dir, "book.azw3")
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"--format", "azw3", "-o", output, input}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v, stderr=%s", err, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("MOBI")) || !bytes.Contains(data, []byte("CLI EPUB")) {
		t.Fatal("CLI did not produce native AZW3 from EPUB")
	}
}

func TestRunInfersAZW3FromOutputExtension(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.txt")
	output := filepath.Join(dir, "book.azw3")
	if err := os.WriteFile(input, []byte("第1章 开始\n正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run([]string{"-o", output, input}, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v, stderr=%s", err, stderr.String())
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("MOBI")) {
		t.Fatal(".azw3 output did not select AZW3 writer")
	}
}

func writeCommandEPUBFixture(t *testing.T, filename string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	files := []struct{ name, content string }{
		{"META-INF/container.xml", `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`},
		{"book.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>CLI EPUB</dc:title></metadata><manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`},
		{"c1.xhtml", `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1>正文</h1><p>内容。</p></body></html>`},
	}
	for _, entry := range files {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entry.content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

package server

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	txtconfig "github.com/flashdict/kindle2flashdict/internal/txt2epub/config"
)

func TestConvertFileUsesNativeEPUBReaderWithoutCalibre(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "book.epub")
	writeServerEPUBFixture(t, input)
	output := filepath.Join(dir, "book.azw3")
	cfg := txtconfig.Defaults()
	if err := convertFile(input, output, "epub", cfg, ConvertOptions{Format: "azw3"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("MOBI")) || !bytes.Contains(data, []byte("服务端 EPUB")) {
		t.Fatal("native EPUB conversion did not produce expected AZW3 metadata")
	}
}

func writeServerEPUBFixture(t *testing.T, filename string) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	files := map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?><container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="book.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`,
		"book.opf":               `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>服务端 EPUB</dc:title><dc:language>zh-CN</dc:language></metadata><manifest><item id="c1" href="c1.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="c1"/></spine></package>`,
		"c1.xhtml":               `<html xmlns="http://www.w3.org/1999/xhtml"><body><h1 id="start">正文</h1><p>内容。</p></body></html>`,
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
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

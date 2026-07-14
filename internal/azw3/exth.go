package azw3

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func buildEXTH(meta ebook.Metadata, coverOffset uint32, resourceCount int) []byte {
	var records [][]byte
	addBytes := func(recordType uint32, data []byte) {
		record := make([]byte, 8+len(data))
		binary.BigEndian.PutUint32(record[0:4], recordType)
		binary.BigEndian.PutUint32(record[4:8], uint32(len(record)))
		copy(record[8:], data)
		records = append(records, record)
	}
	add := func(recordType uint32, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		addBytes(recordType, []byte(value))
	}
	add(503, meta.Title)
	add(100, meta.Author)
	add(524, exthLanguage(meta.Language))
	asin := exthASIN(meta)
	add(113, asin)
	add(112, exthSource(meta))
	add(501, "EBOK")
	var resources [4]byte
	binary.BigEndian.PutUint32(resources[:], uint32(max(0, resourceCount)))
	addBytes(125, resources[:])
	add(528, "true")
	if coverOffset != nullIndex {
		var value [4]byte
		binary.BigEndian.PutUint32(value[:], coverOffset)
		addBytes(201, value[:])
	}

	total := 12
	for _, record := range records {
		total += len(record)
	}
	// Calibre keeps alignment bytes outside the declared EXTH length and emits
	// four bytes even when the payload is already aligned. The MOBI full-name
	// offset still includes these bytes because buildHeaderRecord receives the
	// complete, padded block.
	padding := 4 - total%4
	var w bytes.Buffer
	w.WriteString("EXTH")
	writeUint32(&w, uint32(total))
	writeUint32(&w, uint32(len(records)))
	for _, record := range records {
		w.Write(record)
	}
	for i := 0; i < padding; i++ {
		w.WriteByte(0)
	}
	return w.Bytes()
}

func exthSource(meta ebook.Metadata) string {
	return "kindle-go:" + exthASIN(meta)
}

func exthASIN(meta ebook.Metadata) string {
	basis := strings.TrimSpace(meta.Identifier)
	if basis == "" {
		basis = strings.TrimSpace(meta.Title)
	}
	// UUID v5 uses SHA-1 over a namespace UUID and a stable book identifier.
	// Calibre likewise uses a UUID for EXTH 113; deriving it instead of using a
	// random UUID keeps repeated conversions stable on Kindle.
	urlNamespace := [16]byte{0x6b, 0xa7, 0xb8, 0x11, 0x9d, 0xad, 0x11, 0xd1, 0x80, 0xb4, 0x00, 0xc0, 0x4f, 0xd4, 0x30, 0xc8}
	h := sha1.New()
	_, _ = h.Write(urlNamespace[:])
	_, _ = h.Write([]byte("kindle-go:" + basis))
	sum := h.Sum(nil)
	var uuid [16]byte
	copy(uuid[:], sum)
	uuid[6] = (uuid[6] & 0x0f) | 0x50
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

func exthLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if primary, _, ok := strings.Cut(language, "-"); ok {
		return primary
	}
	if primary, _, ok := strings.Cut(language, "_"); ok {
		return primary
	}
	return language
}

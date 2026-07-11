package azw3

import (
	"bytes"
	"encoding/binary"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func buildEXTH(meta ebook.Metadata, coverOffset uint32) []byte {
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
	add(524, meta.Language)
	add(113, meta.Identifier)
	if coverOffset != nullIndex {
		var value [4]byte
		binary.BigEndian.PutUint32(value[:], coverOffset)
		addBytes(201, value[:])
	}

	total := 12
	for _, record := range records {
		total += len(record)
	}
	padding := (4 - total%4) % 4
	var w bytes.Buffer
	w.WriteString("EXTH")
	writeUint32(&w, uint32(total+padding))
	writeUint32(&w, uint32(len(records)))
	for _, record := range records {
		w.Write(record)
	}
	for i := 0; i < padding; i++ {
		w.WriteByte(0)
	}
	return w.Bytes()
}

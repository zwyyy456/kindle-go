package azw3

import (
	"bytes"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const palmEpochDelta = 2082844800

func writePalmDB(w io.Writer, title string, records []record) error {
	var out bytes.Buffer
	headerSize := 78 + len(records)*8
	offset := headerSize
	writePalmHeader(&out, title, len(records))
	for i, record := range records {
		writeUint32(&out, uint32(offset))
		out.WriteByte(0)
		out.Write([]byte{byte(i >> 16), byte(i >> 8), byte(i)})
		offset += len(record.data)
	}
	for _, record := range records {
		out.Write(record.data)
	}
	_, err := w.Write(out.Bytes())
	return err
}

func writePalmHeader(w *bytes.Buffer, title string, recordCount int) {
	name := palmName(title)
	nameBuf := make([]byte, 32)
	copy(nameBuf, []byte(name))
	w.Write(nameBuf)
	writeUint16(w, 0)
	writeUint16(w, 0)
	now := uint32(time.Now().Unix() + palmEpochDelta)
	writeUint32(w, now)
	writeUint32(w, now)
	writeUint32(w, 0)
	writeUint32(w, 0)
	writeUint32(w, 0)
	writeUint32(w, 0)
	w.WriteString("BOOK")
	w.WriteString("MOBI")
	writeUint32(w, 0)
	writeUint32(w, 0)
	writeUint16(w, uint16(recordCount))
}

func palmName(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled"
	}
	for len([]byte(title)) > 31 {
		_, size := utf8.DecodeLastRuneInString(title)
		if size <= 0 {
			break
		}
		title = title[:len(title)-size]
	}
	return title
}

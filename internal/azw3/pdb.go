package azw3

import (
	"bytes"
	"fmt"
	"io"
	"time"
)

const palmEpochDelta = 2082844800

func writePalmDB(w io.Writer, title string, records []record) error {
	var out bytes.Buffer
	headerSize := 78 + len(records)*8 + 2
	offset := headerSize
	writePalmHeader(&out, title, len(records))
	for i, record := range records {
		writeUint32(&out, uint32(offset))
		out.WriteByte(0)
		uid := i * 2
		out.Write([]byte{byte(uid >> 16), byte(uid >> 8), byte(uid)})
		offset += len(record.data)
	}
	writeUint16(&out, 0)
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
	uniqueIDSeed := uint32(1)
	if recordCount > 0 {
		uniqueIDSeed = uint32(2*recordCount - 1)
	}
	writeUint32(w, uniqueIDSeed)
	writeUint32(w, 0)
	writeUint16(w, uint16(recordCount))
}

func palmName(title string) string {
	return fmt.Sprintf("kindle-go-%08x", stableID(title))
}

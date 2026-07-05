package azw3

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
)

func buildHeaderRecord(c compiledBook, exth []byte) []byte {
	const mobiHeaderLen = 232
	fullName := []byte(c.metadata.Title)
	fullNameOffset := 16 + mobiHeaderLen + len(exth)

	var w bytes.Buffer
	writeUint16(&w, 1)
	writeUint16(&w, 0)
	writeUint32(&w, uint32(len(c.text)))
	writeUint16(&w, uint16(len(c.chunks)))
	writeUint16(&w, textRecordSize)
	writeUint16(&w, 0)
	writeUint16(&w, 0)

	mobi := make([]byte, mobiHeaderLen)
	copy(mobi[0:4], "MOBI")
	binary.BigEndian.PutUint32(mobi[4:8], mobiHeaderLen)
	binary.BigEndian.PutUint32(mobi[8:12], 2)
	binary.BigEndian.PutUint32(mobi[12:16], 65001)
	binary.BigEndian.PutUint32(mobi[16:20], stableID(c.metadata.Identifier+c.metadata.Title))
	binary.BigEndian.PutUint32(mobi[20:24], 8)
	binary.BigEndian.PutUint32(mobi[84:88], uint32(fullNameOffset))
	binary.BigEndian.PutUint32(mobi[88:92], uint32(len(fullName)))
	binary.BigEndian.PutUint32(mobi[108:112], 6)
	binary.BigEndian.PutUint32(mobi[112:116], 0xffffffff)
	binary.BigEndian.PutUint32(mobi[120:124], 0xffffffff)
	binary.BigEndian.PutUint32(mobi[124:128], 0xffffffff)
	binary.BigEndian.PutUint32(mobi[128:132], 0x40)
	binary.BigEndian.PutUint32(mobi[192:196], 0xffffffff)
	binary.BigEndian.PutUint32(mobi[196:200], 0xffffffff)
	binary.BigEndian.PutUint32(mobi[200:204], uint32(1))
	binary.BigEndian.PutUint32(mobi[204:208], uint32(len(c.chunks)))
	w.Write(mobi)
	w.Write(exth)
	w.Write(fullName)
	return w.Bytes()
}

func stableID(value string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	id := h.Sum32()
	if id == 0 {
		return 1
	}
	return id
}

func writeUint16(w *bytes.Buffer, value uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], value)
	w.Write(buf[:])
}

func writeUint32(w *bytes.Buffer, value uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	w.Write(buf[:])
}

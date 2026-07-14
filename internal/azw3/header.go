package azw3

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
)

func buildHeaderRecord(c compiledBook, exth []byte) []byte {
	const mobiHeaderLen = 264
	fullName := []byte(c.metadata.Title)
	fullNameOffset := 16 + mobiHeaderLen + len(exth)

	var w bytes.Buffer
	writeUint16(&w, 2)
	writeUint16(&w, 0)
	writeUint32(&w, uint32(len(c.text)))
	writeUint16(&w, uint16(len(c.records)))
	writeUint16(&w, textRecordSize)
	writeUint16(&w, 0)
	writeUint16(&w, 0)

	mobi := make([]byte, mobiHeaderLen)
	put := func(recordOffset int, value uint32) {
		binary.BigEndian.PutUint32(mobi[recordOffset-16:recordOffset-12], value)
	}
	copy(mobi[0:4], "MOBI")
	put(20, mobiHeaderLen)
	put(24, 2)
	put(28, 65001)
	put(32, stableID(c.metadata.Identifier+c.metadata.Title))
	put(36, 8)
	for off := 40; off < 80; off += 4 {
		put(off, 0xffffffff)
	}
	put(80, uint32(c.firstNonTextRecord))
	put(84, uint32(fullNameOffset))
	put(88, uint32(len(fullName)))
	put(92, languageCode(c.metadata.Language))
	put(104, 8)
	put(108, c.firstResourceRecord)
	// PalmDOC compression has no HUFF/CDIC records. Native Kindle readers
	// expect a zero offset/count pair here; 0xffffffff is treated as a record
	// reference by older Mobi8SDK builds.
	put(112, 0)
	put(116, 0)
	put(120, 0)
	put(124, 0)
	put(128, 0x50)
	put(164, 0xffffffff)
	put(168, 0xffffffff)
	put(172, 0)
	put(176, 0)
	put(180, 0)
	put(192, c.fdstRecord)
	put(196, c.fdstCount)
	put(200, c.fcisRecord)
	put(204, 1)
	put(208, c.flisRecord)
	put(212, 1)
	put(224, 0xffffffff)
	put(228, 0)
	put(232, 0xffffffff)
	put(236, 0xffffffff)
	// Text records carry both the multibyte overlap trailer (bit 0) and the
	// navigation indexing TBS trailer (bit 1).
	put(240, 3)
	put(244, c.ncxIndexRecord)
	put(248, c.chunkIndexRecord)
	put(252, c.skelIndexRecord)
	put(256, nullIndex)
	put(260, c.guideIndexRecord)
	put(264, 0xffffffff)
	put(268, 0)
	put(272, 0xffffffff)
	put(276, 0)
	w.Write(mobi)
	w.Write(exth)
	w.Write(fullName)
	w.Write(bytes.Repeat([]byte{0}, 8192))
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

func languageCode(language string) uint32 {
	switch language {
	case "zh", "zh-CN", "zho", "chi":
		return 0x0804
	case "zh-TW":
		return 0x0404
	case "en", "en-US":
		return 0x0409
	default:
		return 0
	}
}

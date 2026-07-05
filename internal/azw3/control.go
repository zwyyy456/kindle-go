package azw3

import "bytes"

var flisRecord = append([]byte("FLIS\x00\x00\x00\x08\x00\x41\x00\x00\x00\x00\x00\x00\xff\xff\xff\xff\x00\x01\x00\x03\x00\x00\x00\x03\x00\x00\x00\x01"), []byte{0xff, 0xff, 0xff, 0xff}...)

func buildFDST(textLen int) []byte {
	var w bytes.Buffer
	w.WriteString("FDST")
	writeUint32(&w, 12)
	writeUint32(&w, 1)
	writeUint32(&w, 0)
	writeUint32(&w, uint32(textLen))
	return w.Bytes()
}

func buildFCIS(textLen int) []byte {
	var w bytes.Buffer
	w.WriteString("FCIS")
	writeUint32(&w, 0x14)
	writeUint32(&w, 0x10)
	writeUint32(&w, 0x02)
	writeUint32(&w, 0)
	writeUint32(&w, uint32(textLen))
	writeUint32(&w, 0)
	writeUint32(&w, 0)
	writeUint32(&w, 0x28)
	writeUint32(&w, 0)
	writeUint32(&w, 0)
	writeUint32(&w, 0x28)
	writeUint32(&w, 0x08)
	writeUint32(&w, 0x00010001)
	writeUint32(&w, 0)
	return w.Bytes()
}

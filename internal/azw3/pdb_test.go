package azw3

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestPalmDBUsesStableASCIINameAndRecordUIDSeed(t *testing.T) {
	records := []record{{data: []byte("zero")}, {data: []byte("one")}, {data: []byte("two")}}
	var out bytes.Buffer
	if err := writePalmDB(&out, "极具恐怖", records); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()
	name := bytes.TrimRight(data[:32], "\x00")
	if string(name) != palmName("极具恐怖") {
		t.Fatalf("PalmDB name = %q, want %q", name, palmName("极具恐怖"))
	}
	for _, b := range name {
		if b < 0x20 || b > 0x7e {
			t.Fatalf("PalmDB name contains non-ASCII byte %#x", b)
		}
	}
	if got, want := binary.BigEndian.Uint32(data[68:72]), uint32(5); got != want {
		t.Fatalf("unique ID seed = %d, want %d", got, want)
	}
	for i, want := range []uint32{0, 2, 4} {
		off := 78 + i*8 + 5
		got := uint32(data[off])<<16 | uint32(data[off+1])<<8 | uint32(data[off+2])
		if got != want {
			t.Fatalf("record %d UID = %d, want %d", i, got, want)
		}
	}
}

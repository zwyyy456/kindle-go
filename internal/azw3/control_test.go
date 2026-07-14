package azw3

import (
	"encoding/binary"
	"testing"
)

func TestFCISUsesCanonicalKF8Layout(t *testing.T) {
	record := buildFCIS(12345)
	if len(record) != 52 {
		t.Fatalf("FCIS length = %d, want 52", len(record))
	}
	wants := map[int]uint32{
		4: 0x14, 8: 0x10, 12: 2, 16: 0, 20: 12345,
		24: 0, 28: 0x28, 32: 0, 36: 0x28, 40: 8,
		44: 0x00010001, 48: 0,
	}
	for off, want := range wants {
		if got := binary.BigEndian.Uint32(record[off : off+4]); got != want {
			t.Fatalf("FCIS[%d] = %#x, want %#x", off, got, want)
		}
	}
}

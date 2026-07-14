package azw3

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestPalmDOCCompressionRoundTripsAndShrinksRepetitiveText(t *testing.T) {
	input := bytes.Repeat([]byte("<p>中文正文，中文正文，中文正文。</p>"), 80)
	compressed := compressPalmDOC(input)
	if len(compressed) >= len(input) {
		t.Fatalf("compressed size = %d, input size = %d", len(compressed), len(input))
	}
	decoded, err := decompressPalmDOC(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, input) {
		t.Fatal("PalmDOC round trip changed text")
	}
}

func TestPalmDOCCompressionRoundTripsArbitraryBytes(t *testing.T) {
	inputs := [][]byte{
		{0, 1, 2, 8, 9, 0x7f, 0x80, 0xbf, 0xc0, 0xff},
		bytes.Repeat([]byte("aaaaaaaaaaaaaaaa"), 40),
		bytes.Repeat([]byte(" abc ABC 123\x00\xff"), 40),
	}
	rng := rand.New(rand.NewSource(1))
	random := make([]byte, textRecordSize)
	_, _ = rng.Read(random)
	inputs = append(inputs, random)
	for i, input := range inputs {
		compressed := compressPalmDOC(input)
		decoded, err := decompressPalmDOC(compressed)
		if err != nil {
			t.Fatalf("case %d: %v", i, err)
		}
		if !bytes.Equal(decoded, input) {
			t.Fatalf("case %d changed after PalmDOC round trip", i)
		}
	}
}

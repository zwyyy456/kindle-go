package azw3

import (
	"bytes"
	"encoding/hex"
	"math/rand"
	"testing"
)

func TestPalmDOCCompressionMatchesCalibreGoldenBytes(t *testing.T) {
	tests := []struct {
		input []byte
		hex   string
	}{
		{bytes.Repeat([]byte{'a'}, 32), "61616161616161616161618057805761"},
		{[]byte("abcabcabcabcabcabcabcabcabcabc"), "6162636162636162636162804e63616263616263616263"},
		{[]byte("0123456789abcdefghij0123456789abcdefghij"), "303132333435363738396162636465666768696a80a76162636465666768696a"},
		{[]byte{0, 1, 2, 8, 9, 0x7f, 0x80, 0xbf, 0xc0, 0xff}, "0003010208097f0480bfc0ff"},
	}
	for i, test := range tests {
		want, err := hex.DecodeString(test.hex)
		if err != nil {
			t.Fatal(err)
		}
		if got := compressPalmDOC(test.input); !bytes.Equal(got, want) {
			t.Fatalf("case %d compressed = %x, want Calibre bytes %x", i, got, want)
		}
	}
}

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

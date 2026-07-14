package azw3

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestChunkBytesUsesFixedRecordsAndUTF8Overlap(t *testing.T) {
	prefix := bytes.Repeat([]byte{'a'}, textRecordSize-1)
	text := append(prefix, []byte("中文")...)
	records := chunkBytes(text, textRecordSize)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if len(records[0].data) != textRecordSize {
		t.Fatalf("first record length = %d, want %d", len(records[0].data), textRecordSize)
	}
	if got, want := records[0].overlap, []byte("中")[1:]; !bytes.Equal(got, want) {
		t.Fatalf("first overlap = %x, want %x", got, want)
	}
	if !utf8.Valid(append(append([]byte{}, records[0].data...), records[0].overlap...)) {
		t.Fatal("record plus overlap is not valid UTF-8")
	}
	var rebuilt []byte
	for _, record := range records {
		rebuilt = append(rebuilt, record.data...)
	}
	if !bytes.Equal(rebuilt, text) {
		t.Fatal("fixed-size record payloads did not reconstruct the source text")
	}
}

func TestBuildIndexingTBSMatchesFlatGoldenBytes(t *testing.T) {
	entries := []ncxEntry{
		{index: 0, offset: 0, length: 3000, depth: 0, parent: -1},
		{index: 1, offset: 3000, length: 3000, depth: 0, parent: -1},
		{index: 2, offset: 6000, length: 2000, depth: 0, parent: -1},
	}
	got, err := buildIndexingTBS(entries, []int{4096, 3904})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{mustDecodeHex(t, "868802"), mustDecodeHex(t, "8e8802")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TBS = %x, want %x", got, want)
	}
}

func TestBuildIndexingTBSMatchesNestedGoldenBytes(t *testing.T) {
	entries := []ncxEntry{
		{index: 0, offset: 0, length: 7000, depth: 0, parent: -1, firstChild: 1, lastChild: 2},
		{index: 1, offset: 100, length: 3000, depth: 1, parent: 0, firstChild: -1, lastChild: -1},
		{index: 2, offset: 3100, length: 3900, depth: 1, parent: 0, firstChild: -1, lastChild: -1},
		{index: 3, offset: 7000, length: 2000, depth: 0, parent: -1, firstChild: -1, lastChild: -1},
	}
	got, err := buildIndexingTBS(entries, []int{4096, 4096, 808})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{mustDecodeHex(t, "82859402"), mustDecodeHex(t, "8285a090"), mustDecodeHex(t, "9a85")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nested TBS = %x, want %x", got, want)
	}
}

func TestTrailingDataLengthRoundTrips(t *testing.T) {
	for _, size := range []int{0, 1, 4, 126, 127, 128, 16384} {
		raw := bytes.Repeat([]byte{0x5a}, size)
		encoded := encodeTrailingData(raw)
		payload, got := stripIndexingTrailerForTest(t, encoded)
		if len(payload) != 0 || !bytes.Equal(got, raw) {
			t.Fatalf("size %d decoded payload=%x trailer length=%d", size, payload, len(got))
		}
	}
}

func mustDecodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestUTF8OverlapIgnoresContinuationAtRecordStart(t *testing.T) {
	text := append(bytes.Repeat([]byte("中"), 2731), []byte("结尾")...)
	records := chunkBytes(text, textRecordSize)
	if len(records) < 2 || records[1].data[0]&0xc0 != 0x80 {
		t.Fatal("fixture did not place a UTF-8 continuation byte at the second record start")
	}
	for i, record := range records[:len(records)-1] {
		if len(record.data) != textRecordSize {
			t.Fatalf("record %d length = %d, want %d", i, len(record.data), textRecordSize)
		}
		if len(record.overlap) > utf8.UTFMax-1 {
			t.Fatalf("record %d overlap is too long: %d", i, len(record.overlap))
		}
	}
}

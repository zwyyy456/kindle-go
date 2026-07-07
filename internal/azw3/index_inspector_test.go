package azw3

import (
	"bytes"
	"encoding/binary"
	"os"
	"strconv"
	"testing"
)

type inspectedMOBIHeader struct {
	firstNonText uint32
	ncxIndex     uint32
	chunkIndex   uint32
	skelIndex    uint32
	guideIndex   uint32
}

type inspectedTag struct {
	number         byte
	valuesPerEntry int
	bitmask        byte
	end            bool
}

type inspectedIndexEntry struct {
	lead string
	tags map[byte][]int
}

type inspectedIndex struct {
	entries     []inspectedIndexEntry
	cncxRecords [][]byte
}

func readAZW3Records(t *testing.T, path string) [][]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 78 {
		t.Fatalf("palm db too short: %d", len(data))
	}
	count := int(binary.BigEndian.Uint16(data[76:78]))
	if count == 0 {
		t.Fatal("record count is zero")
	}
	offsets := make([]int, count+1)
	for i := 0; i < count; i++ {
		pos := 78 + i*8
		if pos+4 > len(data) {
			t.Fatalf("record table truncated at record %d", i)
		}
		offsets[i] = int(binary.BigEndian.Uint32(data[pos : pos+4]))
	}
	offsets[count] = len(data)
	records := make([][]byte, count)
	for i := 0; i < count; i++ {
		if offsets[i] < 0 || offsets[i] > offsets[i+1] || offsets[i+1] > len(data) {
			t.Fatalf("bad record offsets %d: %d..%d", i, offsets[i], offsets[i+1])
		}
		records[i] = data[offsets[i]:offsets[i+1]]
	}
	return records
}

func inspectAZW3MOBIHeader(t *testing.T, record []byte) inspectedMOBIHeader {
	t.Helper()
	if len(record) < 280 {
		t.Fatalf("record 0 too short: %d", len(record))
	}
	if string(record[16:20]) != "MOBI" {
		t.Fatalf("record 0 missing MOBI: %q", record[16:20])
	}
	u32 := func(recordOffset int) uint32 {
		return binary.BigEndian.Uint32(record[recordOffset : recordOffset+4])
	}
	return inspectedMOBIHeader{
		firstNonText: u32(80),
		ncxIndex:     u32(244),
		chunkIndex:   u32(248),
		skelIndex:    u32(252),
		guideIndex:   u32(260),
	}
}

func inspectAZW3Index(t *testing.T, records [][]byte, recordIndex uint32) inspectedIndex {
	t.Helper()
	if recordIndex == nullIndex {
		t.Fatal("index record is null")
	}
	start := int(recordIndex)
	if start+1 >= len(records) {
		t.Fatalf("index record %d points past records", recordIndex)
	}
	header := records[start]
	if !bytes.HasPrefix(header, []byte("INDX")) {
		t.Fatalf("index header %d has prefix %q", start, header[:min(len(header), 8)])
	}
	tags, controlByteCount := inspectTAGX(t, header)
	numCNCX := int(readIndexUint32(t, header, 52))
	entryRecord := records[start+1]
	entries := inspectIndexEntries(t, entryRecord, tags, controlByteCount)
	if start+2+numCNCX > len(records) {
		t.Fatalf("index %d CNCX records exceed record count: %d", start, numCNCX)
	}
	cncxRecords := make([][]byte, numCNCX)
	copy(cncxRecords, records[start+2:start+2+numCNCX])
	return inspectedIndex{entries: entries, cncxRecords: cncxRecords}
}

func inspectTAGX(t *testing.T, record []byte) ([]inspectedTag, int) {
	t.Helper()
	if len(record) < 204 || string(record[192:196]) != "TAGX" {
		t.Fatalf("index header missing TAGX at 192")
	}
	tableLen := int(readIndexUint32(t, record, 196))
	controlByteCount := int(readIndexUint32(t, record, 200))
	if tableLen < 12 || 192+tableLen > len(record) {
		t.Fatalf("bad TAGX table length: %d", tableLen)
	}
	var tags []inspectedTag
	for off := 204; off+4 <= 192+tableLen; off += 4 {
		tag := inspectedTag{
			number:         record[off],
			valuesPerEntry: int(record[off+1]),
			bitmask:        record[off+2],
			end:            record[off+3] != 0,
		}
		if tag.end {
			break
		}
		tags = append(tags, tag)
	}
	if controlByteCount <= 0 {
		t.Fatalf("bad TAGX control byte count: %d", controlByteCount)
	}
	return tags, controlByteCount
}

func inspectIndexEntries(t *testing.T, record []byte, tags []inspectedTag, controlByteCount int) []inspectedIndexEntry {
	t.Helper()
	if !bytes.HasPrefix(record, []byte("INDX")) {
		t.Fatalf("index record has prefix %q", record[:min(len(record), 8)])
	}
	idxtOffset := int(readIndexUint32(t, record, 20))
	entryCount := int(readIndexUint32(t, record, 24))
	if idxtOffset < 192 || idxtOffset+4+entryCount*2 > len(record) {
		t.Fatalf("bad IDXT offset/count: offset=%d count=%d len=%d", idxtOffset, entryCount, len(record))
	}
	if string(record[idxtOffset:idxtOffset+4]) != "IDXT" {
		t.Fatalf("index record missing IDXT at %d", idxtOffset)
	}
	offsets := make([]int, entryCount)
	for i := 0; i < entryCount; i++ {
		offsets[i] = int(binary.BigEndian.Uint16(record[idxtOffset+4+i*2 : idxtOffset+6+i*2]))
	}
	entries := make([]inspectedIndexEntry, 0, entryCount)
	for i, start := range offsets {
		end := idxtOffset
		if i+1 < len(offsets) {
			end = offsets[i+1]
		}
		if start < 192 || start >= end || end > len(record) {
			t.Fatalf("bad index entry %d range: %d..%d", i, start, end)
		}
		entries = append(entries, inspectIndexEntry(t, record[start:end], tags, controlByteCount))
	}
	return entries
}

func inspectIndexEntry(t *testing.T, data []byte, tags []inspectedTag, controlByteCount int) inspectedIndexEntry {
	t.Helper()
	if len(data) == 0 {
		t.Fatal("empty index entry")
	}
	leadLen := int(data[0])
	if 1+leadLen+controlByteCount > len(data) {
		t.Fatalf("short index entry: lead=%d control=%d len=%d", leadLen, controlByteCount, len(data))
	}
	entry := inspectedIndexEntry{
		lead: string(data[1 : 1+leadLen]),
		tags: map[byte][]int{},
	}
	pos := 1 + leadLen
	control := data[pos : pos+controlByteCount]
	pos += controlByteCount
	for _, tag := range tags {
		count := tagValueCount(control, tag)
		total := count * tag.valuesPerEntry
		if total == 0 {
			continue
		}
		values := make([]int, 0, total)
		for i := 0; i < total; i++ {
			value, n, ok := decodeIndexInt(data[pos:])
			if !ok {
				t.Fatalf("cannot decode tag %d value %d in entry %q", tag.number, i, entry.lead)
			}
			values = append(values, value)
			pos += n
		}
		entry.tags[tag.number] = values
	}
	return entry
}

func (idx inspectedIndex) cncxString(t *testing.T, offset int) string {
	t.Helper()
	if offset < 0 {
		t.Fatalf("negative CNCX offset: %d", offset)
	}
	recordIndex := offset / 0x10000
	local := offset % 0x10000
	if recordIndex >= len(idx.cncxRecords) {
		t.Fatalf("CNCX offset %d points past %d records", offset, len(idx.cncxRecords))
	}
	record := idx.cncxRecords[recordIndex]
	if local >= len(record) {
		t.Fatalf("CNCX local offset %d past record len %d", local, len(record))
	}
	size, n, ok := decodeIndexInt(record[local:])
	if !ok {
		t.Fatalf("cannot decode CNCX string length at offset %d", offset)
	}
	start := local + n
	end := start + size
	if end > len(record) {
		t.Fatalf("CNCX string at %d exceeds record len: %d..%d of %d", offset, start, end, len(record))
	}
	return string(record[start:end])
}

func tagValueCount(control []byte, tag inspectedTag) int {
	if tag.bitmask == 0 || len(control) == 0 {
		return 0
	}
	return int((control[0] & tag.bitmask) >> maskShift(tag.bitmask))
}

func decodeIndexInt(data []byte) (int, int, bool) {
	value := 0
	for i, b := range data {
		value = (value << 7) | int(b&0x7f)
		if b&0x80 != 0 {
			return value, i + 1, true
		}
	}
	return 0, 0, false
}

func readIndexUint32(t *testing.T, data []byte, off int) uint32 {
	t.Helper()
	if off+4 > len(data) {
		t.Fatalf("uint32 offset %d past len %d", off, len(data))
	}
	return binary.BigEndian.Uint32(data[off : off+4])
}

func requiredTag(t *testing.T, entry inspectedIndexEntry, number byte, label string) []int {
	t.Helper()
	values := entry.tags[number]
	if len(values) == 0 {
		t.Fatalf("entry %q missing %s tag %d", entry.lead, label, number)
	}
	return values
}

func parseIndexLeadInt(t *testing.T, entry inspectedIndexEntry) int {
	t.Helper()
	value, err := strconv.Atoi(entry.lead)
	if err != nil {
		t.Fatalf("entry lead %q is not an int: %v", entry.lead, err)
	}
	return value
}

func findInspectedNCXEntry(t *testing.T, idx inspectedIndex, label string) inspectedIndexEntry {
	t.Helper()
	for _, entry := range idx.entries {
		labelOffset := requiredTag(t, entry, 3, "label")[0]
		if idx.cncxString(t, labelOffset) == label {
			return entry
		}
	}
	t.Fatalf("missing NCX entry label %q", label)
	return inspectedIndexEntry{}
}

func inspectedChunkBySequence(t *testing.T, entries []inspectedIndexEntry, seq int) inspectedIndexEntry {
	t.Helper()
	for _, entry := range entries {
		values := requiredTag(t, entry, 4, "sequence_number")
		if values[0] == seq {
			return entry
		}
	}
	t.Fatalf("missing chunk sequence %d", seq)
	return inspectedIndexEntry{}
}

func inspectChunkRawBySequence(t *testing.T, text []byte, skels []inspectedIndexEntry, chunks []inspectedIndexEntry, seq int) []byte {
	t.Helper()
	for docIndex, skel := range skels {
		geometry := requiredTag(t, skel, 6, "skel geometry")
		if len(geometry) < 2 {
			t.Fatalf("skel %q geometry too short: %v", skel.lead, geometry)
		}
		rawStart := geometry[0] + geometry[1]
		docChunks := chunksForFileNumber(t, chunks, docIndex)
		for _, chunk := range docChunks {
			chunkSeq := requiredTag(t, chunk, 4, "sequence_number")[0]
			chunkGeometry := requiredTag(t, chunk, 6, "chunk geometry")
			if len(chunkGeometry) < 2 {
				t.Fatalf("chunk %q geometry too short: %v", chunk.lead, chunkGeometry)
			}
			rawEnd := rawStart + chunkGeometry[1]
			if rawStart < 0 || rawEnd > len(text) {
				t.Fatalf("chunk seq %d raw range out of text: %d..%d len=%d", chunkSeq, rawStart, rawEnd, len(text))
			}
			if chunkSeq == seq {
				return text[rawStart:rawEnd]
			}
			rawStart = rawEnd
		}
	}
	t.Fatalf("missing raw chunk sequence %d", seq)
	return nil
}

func chunksForFileNumber(t *testing.T, chunks []inspectedIndexEntry, fileNumber int) []inspectedIndexEntry {
	t.Helper()
	var out []inspectedIndexEntry
	for _, chunk := range chunks {
		values := requiredTag(t, chunk, 3, "file_number")
		if values[0] == fileNumber {
			out = append(out, chunk)
		}
	}
	return out
}

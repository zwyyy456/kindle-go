package azw3

import "github.com/flashdict/kindle2flashdict/internal/ebook"

const textRecordSize = 4096
const nullIndex = 0xffffffff

type compiledBook struct {
	metadata ebook.Metadata
	style    ebook.Style

	text       []byte
	records    []textRecord
	flowBounds [][2]int
	documents  []compiledDocument
	targets    map[string]target

	skelTable  []skelEntry
	chunkTable []chunkEntry
	tocTable   []ncxEntry
	guideTable []guideEntry
	resources  []compiledResource

	firstNonTextRecord  int
	chunkIndexRecord    uint32
	skelIndexRecord     uint32
	guideIndexRecord    uint32
	ncxIndexRecord      uint32
	fdstRecord          uint32
	flisRecord          uint32
	fcisRecord          uint32
	fdstCount           uint32
	firstResourceRecord uint32
	coverResourceOffset uint32
}

type compiledResource struct {
	mediaType string
	data      []byte
}

type record struct {
	data []byte
}

type textRecord struct {
	data    []byte
	overlap []byte
	start   int
}

type compiledDocument struct {
	href       string
	title      string
	skeleton   []byte
	chunks     []contentChunk
	aidByID    map[string]string
	bodyAID    string
	flowStart  int
	rebuildLen int
}

type contentChunk struct {
	seq       int
	raw       []byte
	insertPos int
	startPos  int
	length    int
	selector  string
}

type target struct {
	aid            string
	absoluteOffset int
	chunkSeq       int
	chunkOffset    int
}

type skelEntry struct {
	name       string
	chunkCount int
	startPos   int
	length     int
}

type chunkEntry struct {
	insertPos      int
	selector       string
	fileNumber     int
	sequenceNumber int
	startPos       int
	length         int
}

type ncxEntry struct {
	index      int
	label      string
	depth      int
	offset     int
	length     int
	posFID     [2]int
	parent     int
	firstChild int
	lastChild  int
}

type guideEntry struct {
	title  string
	typ    string
	posFID [2]int
}

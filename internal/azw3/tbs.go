package azw3

import (
	"fmt"
	"sort"
)

const (
	tbsEndsOrSpansFlag = 1
	tbsLengthFlag      = 2
	tbsCountFlag       = 4
	tbsNewStrandFlag   = 8
)

type tbsEntry struct {
	index, start, length                    int
	depth, parent, firstChild, lastChild    int
	title, action                           string
	startOffset, lengthOffset, recordLength int
}

type tbsLayer struct {
	depth   int
	entries []*tbsEntry
}

type tbsStrand []tbsLayer

type tbsSequence struct {
	value int
	extra map[int]int
}

func buildIndexingTBS(entries []ncxEntry, recordLengths []int) ([][]byte, error) {
	base := make([]*tbsEntry, 0, len(entries))
	for _, entry := range entries {
		base = append(base, &tbsEntry{
			index:      entry.index,
			start:      entry.offset,
			length:     entry.length,
			depth:      entry.depth,
			parent:     entry.parent,
			firstChild: entry.firstChild,
			lastChild:  entry.lastChild,
			title:      entry.label,
		})
	}
	indexing := collectTBSIndexingData(base, recordLengths)
	encoded, err := calculateAllTBS(indexing, 8)
	if err != nil {
		encoded, err = calculateAllTBS(indexing, 5)
	}
	return encoded, err
}

func collectTBSIndexingData(entries []*tbsEntry, recordLengths []int) [][]tbsStrand {
	sorted := append([]*tbsEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].start < sorted[j].start })
	data := make([][]tbsStrand, 0, len(recordLengths))
	recordStart := 0
	for _, recordLength := range recordLengths {
		nextRecordStart := recordStart + recordLength
		var local []*tbsEntry
		for _, entry := range sorted {
			if entry.start >= nextRecordStart {
				break
			}
			if entry.start+entry.length <= recordStart {
				continue
			}
			local = append(local, fillTBSEntry(entry, entry.start-recordStart, recordLength))
		}
		data = append(data, separateTBSStrands(local))
		recordStart = nextRecordStart
	}
	return data
}

func fillTBSEntry(entry *tbsEntry, startOffset, recordLength int) *tbsEntry {
	copy := *entry
	copy.startOffset = startOffset
	copy.lengthOffset = startOffset + entry.length
	copy.recordLength = recordLength
	switch {
	case startOffset < 0 && copy.lengthOffset > recordLength:
		copy.action = "spans"
	case startOffset < 0:
		copy.action = "ends"
	case copy.lengthOffset > recordLength:
		copy.action = "starts"
	default:
		copy.action = "completes"
	}
	return &copy
}

func separateTBSStrands(entries []*tbsEntry) []tbsStrand {
	remaining := append([]*tbsEntry(nil), entries...)
	var strands []tbsStrand
	for len(remaining) > 0 {
		top := remaining[0]
		remaining = remaining[1:]
		strandEntries := populateTBSStrand(top, &remaining)
		var strand tbsStrand
		for _, entry := range strandEntries {
			layer := -1
			for i := range strand {
				if strand[i].depth == entry.depth {
					layer = i
					break
				}
			}
			if layer < 0 {
				strand = append(strand, tbsLayer{depth: entry.depth})
				layer = len(strand) - 1
			}
			strand[layer].entries = append(strand[layer].entries, entry)
		}
		strands = append(strands, strand)
	}
	return strands
}

func populateTBSStrand(parent *tbsEntry, entries *[]*tbsEntry) []*tbsEntry {
	answer := []*tbsEntry{parent}
	children := tbsChildren(*entries, parent.index)
	if len(children) > 0 {
		child := children[0]
		removeTBSEntry(entries, child)
		return append(answer, populateTBSStrand(child, entries)...)
	}

	currentIndex := parent.index
	var siblings []*tbsEntry
	for _, entry := range append([]*tbsEntry(nil), (*entries)...) {
		if entry.depth != parent.depth || entry.parent != parent.parent || entry.index != currentIndex+1 {
			continue
		}
		currentIndex++
		removeTBSEntry(entries, entry)
		children = tbsChildren(*entries, entry.index)
		if len(children) > 0 {
			siblings = append(siblings, populateTBSStrand(entry, entries)...)
			break
		}
		siblings = append(siblings, entry)
	}
	return append(answer, siblings...)
}

func tbsChildren(entries []*tbsEntry, parent int) []*tbsEntry {
	var children []*tbsEntry
	for _, entry := range entries {
		if entry.parent == parent {
			children = append(children, entry)
		}
	}
	return children
}

func removeTBSEntry(entries *[]*tbsEntry, target *tbsEntry) {
	for i, entry := range *entries {
		if entry == target {
			*entries = append((*entries)[:i], (*entries)[i+1:]...)
			return
		}
	}
}

func calculateAllTBS(indexing [][]tbsStrand, tbsType int) ([][]byte, error) {
	out := make([][]byte, len(indexing))
	for i, strands := range indexing {
		sequences, err := encodeTBSStrands(strands, tbsType)
		if err != nil {
			return nil, err
		}
		out[i] = tbsSequencesToBytes(sequences)
	}
	return out, nil
}

func encodeTBSStrands(strands []tbsStrand, tbsType int) ([]tbsSequence, error) {
	var firstEntry *tbsEntry
	for _, strand := range strands {
		for _, layer := range strand {
			for _, entry := range layer.entries {
				if firstEntry == nil {
					firstEntry = entry
				}
			}
		}
	}

	var answer []tbsSequence
	lastIndex := 0
	for _, strand := range strands {
		var strandSequences []tbsSequence
		for _, layer := range strand {
			entries := layer.entries
			if len(entries) == 0 {
				continue
			}
			extra := map[int]int{}
			if entries[len(entries)-1].action == "spans" {
				extra[tbsEndsOrSpansFlag] = 0
			}
			if entries[0] == firstEntry {
				extra[tbsLengthFlag] = tbsType
			}
			if len(entries) > 1 {
				extra[tbsCountFlag] = len(entries)
			}
			parent := entries[0].parent
			if parent < 0 {
				parent = 0
			}
			value := entries[0].index - parent
			if len(answer) > 0 && len(strandSequences) == 0 {
				value = lastIndex - entries[0].index
				if value < 0 {
					if tbsType != 5 {
						return nil, fmt.Errorf("negative TBS strand index")
					}
					value = -value
				} else {
					extra[tbsNewStrandFlag] = 1
				}
			}
			lastIndex = entries[len(entries)-1].index
			strandSequences = append(strandSequences, tbsSequence{value: value, extra: extra})
		}
		for i := 0; i+1 < len(strandSequences); i++ {
			_, currentSpans := strandSequences[i].extra[tbsEndsOrSpansFlag]
			_, nextSpans := strandSequences[i+1].extra[tbsEndsOrSpansFlag]
			if currentSpans && nextSpans {
				delete(strandSequences[i].extra, tbsEndsOrSpansFlag)
			}
		}
		answer = append(answer, strandSequences...)
	}
	return answer, nil
}

func tbsSequencesToBytes(sequences []tbsSequence) []byte {
	var out []byte
	flagSize := 3
	for _, sequence := range sequences {
		flags := 0
		for flag := range sequence.extra {
			flags |= flag
		}
		flags &= (1 << flagSize) - 1
		out = append(out, encodeForwardVWI(sequence.value<<flagSize|flags)...)
		if value, ok := sequence.extra[tbsLengthFlag]; ok {
			out = append(out, encodeForwardVWI(value)...)
		}
		if value, ok := sequence.extra[tbsCountFlag]; ok {
			out = append(out, byte(value))
		}
		if value, ok := sequence.extra[tbsEndsOrSpansFlag]; ok {
			out = append(out, encodeForwardVWI(value)...)
		}
		flagSize = 4
	}
	return out
}

func encodeForwardVWI(value int) []byte {
	if value < 0 {
		panic("cannot encode a negative variable-width integer")
	}
	var reversed []byte
	for {
		reversed = append(reversed, byte(value&0x7f))
		value >>= 7
		if value == 0 {
			break
		}
	}
	reversed[0] |= 0x80
	out := make([]byte, len(reversed))
	for i := range reversed {
		out[len(reversed)-1-i] = reversed[i]
	}
	return out
}

func encodeTrailingData(raw []byte) []byte {
	for lengthSize := 1; ; lengthSize++ {
		encoded := encodeBackwardVWI(len(raw) + lengthSize)
		if len(encoded) == lengthSize {
			out := append([]byte{}, raw...)
			return append(out, encoded...)
		}
	}
}

func encodeBackwardVWI(value int) []byte {
	if value < 0 {
		panic("cannot encode a negative variable-width integer")
	}
	var reversed []byte
	for {
		reversed = append(reversed, byte(value&0x7f))
		value >>= 7
		if value == 0 {
			break
		}
	}
	reversed[len(reversed)-1] |= 0x80
	out := make([]byte, len(reversed))
	for i := range reversed {
		out[len(reversed)-1-i] = reversed[i]
	}
	return out
}

package azw3

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

type tagMeta struct {
	name           string
	number         byte
	valuesPerEntry int
	bitmask        byte
	end            bool
}

type indexEntry struct {
	lead string
	tags map[string][]int
}

func buildSkelIndex(entries []skelEntry) [][]byte {
	idxEntries := make([]indexEntry, 0, len(entries))
	for _, entry := range entries {
		idxEntries = append(idxEntries, indexEntry{
			lead: entry.name,
			tags: map[string][]int{
				"chunk_count": {entry.chunkCount, entry.chunkCount},
				"geometry":    {entry.startPos, entry.length, entry.startPos, entry.length},
			},
		})
	}
	return buildIndex(idxEntries, []tagMeta{
		{name: "chunk_count", number: 1, valuesPerEntry: 1, bitmask: 3},
		{name: "geometry", number: 6, valuesPerEntry: 2, bitmask: 12},
		{end: true},
	}, nil)
}

func buildChunkIndex(entries []chunkEntry) [][]byte {
	cncx := newCNCX()
	for _, entry := range entries {
		cncx.add(entry.selector)
	}
	idxEntries := make([]indexEntry, 0, len(entries))
	for _, entry := range entries {
		idxEntries = append(idxEntries, indexEntry{
			lead: fmt.Sprintf("%010d", entry.insertPos),
			tags: map[string][]int{
				"cncx_offset":     {cncx.offset(entry.selector)},
				"file_number":     {entry.fileNumber},
				"sequence_number": {entry.sequenceNumber},
				"geometry":        {entry.startPos, entry.length},
			},
		})
	}
	return buildIndex(idxEntries, []tagMeta{
		{name: "cncx_offset", number: 2, valuesPerEntry: 1, bitmask: 1},
		{name: "file_number", number: 3, valuesPerEntry: 1, bitmask: 2},
		{name: "sequence_number", number: 4, valuesPerEntry: 1, bitmask: 4},
		{name: "geometry", number: 6, valuesPerEntry: 2, bitmask: 8},
		{end: true},
	}, cncx.records())
}

func buildGuideIndex(entries []guideEntry) [][]byte {
	if len(entries) == 0 {
		return nil
	}
	cncx := newCNCX()
	for _, entry := range entries {
		cncx.add(entry.title)
	}
	idxEntries := make([]indexEntry, 0, len(entries))
	for _, entry := range entries {
		idxEntries = append(idxEntries, indexEntry{
			lead: entry.typ,
			tags: map[string][]int{
				"title":   {cncx.offset(entry.title)},
				"pos_fid": {entry.posFID[0], entry.posFID[1]},
			},
		})
	}
	return buildIndex(idxEntries, []tagMeta{
		{name: "title", number: 1, valuesPerEntry: 1, bitmask: 1},
		{name: "pos_fid", number: 6, valuesPerEntry: 2, bitmask: 2},
		{end: true},
	}, cncx.records())
}

func buildNCXIndex(entries []ncxEntry) [][]byte {
	if len(entries) == 0 {
		return nil
	}
	cncx := newCNCX()
	for _, entry := range entries {
		cncx.add(entry.label)
	}
	idxEntries := make([]indexEntry, 0, len(entries))
	for _, entry := range entries {
		tags := map[string][]int{
			"offset":  {entry.offset},
			"length":  {entry.length},
			"label":   {cncx.offset(entry.label)},
			"depth":   {entry.depth},
			"pos_fid": {entry.posFID[0], entry.posFID[1]},
		}
		if entry.parent >= 0 {
			tags["parent"] = []int{entry.parent}
		}
		if entry.firstChild >= 0 {
			tags["first_child"] = []int{entry.firstChild}
		}
		if entry.lastChild >= 0 {
			tags["last_child"] = []int{entry.lastChild}
		}
		idxEntries = append(idxEntries, indexEntry{
			lead: fmt.Sprintf("%02X", entry.index),
			tags: tags,
		})
	}
	return buildIndex(idxEntries, []tagMeta{
		{name: "offset", number: 1, valuesPerEntry: 1, bitmask: 1},
		{name: "length", number: 2, valuesPerEntry: 1, bitmask: 2},
		{name: "label", number: 3, valuesPerEntry: 1, bitmask: 4},
		{name: "depth", number: 4, valuesPerEntry: 1, bitmask: 8},
		{name: "parent", number: 21, valuesPerEntry: 1, bitmask: 16},
		{name: "first_child", number: 22, valuesPerEntry: 1, bitmask: 32},
		{name: "last_child", number: 23, valuesPerEntry: 1, bitmask: 64},
		{name: "pos_fid", number: 6, valuesPerEntry: 2, bitmask: 128},
		{end: true},
	}, cncx.records())
}

func buildIndex(entries []indexEntry, tags []tagMeta, cncxRecords [][]byte) [][]byte {
	if len(entries) == 0 {
		return nil
	}
	body := bytes.Buffer{}
	idxt := bytes.Buffer{}
	for _, entry := range entries {
		writeUint16(&idxt, uint16(192+body.Len()))
		lead := []byte(entry.lead)
		body.WriteByte(byte(len(lead)))
		body.Write(lead)
		body.Write(controlBytes(entry, tags))
		for _, tag := range tags {
			if tag.end {
				continue
			}
			for _, value := range entry.tags[tag.name] {
				body.Write(encint(value))
			}
		}
	}
	indexBlock := align4(body.Bytes())
	idxtBlock := align4(append([]byte("IDXT"), idxt.Bytes()...))
	indexRecord := bytes.Buffer{}
	indexRecord.WriteString("INDX")
	writeUint32(&indexRecord, 192)
	writeUint32(&indexRecord, 0)
	writeUint32(&indexRecord, 1)
	writeUint32(&indexRecord, 0)
	writeUint32(&indexRecord, uint32(192+len(indexBlock)))
	writeUint32(&indexRecord, uint32(len(entries)))
	indexRecord.Write(bytes.Repeat([]byte{0xff}, 8))
	indexRecord.Write(bytes.Repeat([]byte{0}, 156))
	indexRecord.Write(indexBlock)
	indexRecord.Write(idxtBlock)

	tagx := align4(buildTAGX(tags))
	geometry := bytes.Buffer{}
	headerIDXT := bytes.Buffer{}
	headerIDXT.WriteString("IDXT")
	writeUint16(&headerIDXT, uint16(192+len(tagx)))
	lastLead := []byte(entries[len(entries)-1].lead)
	geometry.WriteByte(byte(len(lastLead)))
	geometry.Write(lastLead)
	writeUint16(&geometry, uint16(len(entries)))
	geometryBlock := align4(geometry.Bytes())
	headerIDXTBlock := align4(headerIDXT.Bytes())

	header := bytes.Buffer{}
	header.WriteString("INDX")
	writeUint32(&header, 192)
	header.Write(bytes.Repeat([]byte{0}, 8))
	writeUint32(&header, 2)
	writeUint32(&header, uint32(192+len(tagx)+len(geometryBlock)))
	writeUint32(&header, 1)
	writeUint32(&header, 65001)
	writeUint32(&header, nullIndex)
	writeUint32(&header, uint32(len(entries)))
	writeUint32(&header, 0)
	writeUint32(&header, 0)
	writeUint32(&header, 0)
	writeUint32(&header, uint32(len(cncxRecords)))
	header.Write(bytes.Repeat([]byte{0}, 124))
	writeUint32(&header, 192)
	header.Write(bytes.Repeat([]byte{0}, 8))
	header.Write(tagx)
	header.Write(geometryBlock)
	header.Write(headerIDXTBlock)

	records := [][]byte{align4(header.Bytes()), indexRecord.Bytes()}
	records = append(records, cncxRecords...)
	return records
}

func controlBytes(entry indexEntry, tags []tagMeta) []byte {
	var b byte
	for _, tag := range tags {
		if tag.end {
			return []byte{b}
		}
		values := entry.tags[tag.name]
		count := 0
		if tag.valuesPerEntry > 0 {
			count = len(values) / tag.valuesPerEntry
		}
		b |= tag.bitmask & byte(count<<maskShift(tag.bitmask))
	}
	return []byte{b}
}

func buildTAGX(tags []tagMeta) []byte {
	out := bytes.Buffer{}
	out.WriteString("TAGX")
	tableLen := 12 + len(tags)*4
	writeUint32(&out, uint32(tableLen))
	writeUint32(&out, 1)
	for _, tag := range tags {
		out.WriteByte(tag.number)
		out.WriteByte(byte(tag.valuesPerEntry))
		out.WriteByte(tag.bitmask)
		if tag.end {
			out.WriteByte(1)
		} else {
			out.WriteByte(0)
		}
	}
	return out.Bytes()
}

func buildNCXTable(toc []ebook.TOCEntry, targets map[string]target, textLen int) []ncxEntry {
	var flat []ncxEntry
	var walk func([]ebook.TOCEntry, int, int) []int
	walk = func(entries []ebook.TOCEntry, depth, parent int) []int {
		indexes := make([]int, 0, len(entries))
		for _, entry := range entries {
			target := targetForHref(entry.Href, targets)
			idx := len(flat)
			label := strings.TrimSpace(entry.Title)
			if label == "" {
				label = entry.Href
			}
			flat = append(flat, ncxEntry{
				index:      idx,
				label:      label,
				depth:      depth,
				offset:     target.absoluteOffset,
				posFID:     [2]int{target.chunkSeq, target.chunkOffset},
				parent:     parent,
				firstChild: -1,
				lastChild:  -1,
			})
			indexes = append(indexes, idx)
			children := walk(entry.Children, depth+1, idx)
			if len(children) > 0 {
				flat[idx].firstChild = children[0]
				flat[idx].lastChild = children[len(children)-1]
			}
		}
		return indexes
	}
	walk(toc, 0, -1)
	for i := range flat {
		next := textLen
		for _, other := range flat {
			if other.depth <= flat[i].depth && other.offset > flat[i].offset && other.offset < next {
				next = other.offset
			}
		}
		flat[i].length = max(0, next-flat[i].offset)
	}
	return flat
}

func buildGuideTable(guide []ebook.GuideRef, targets map[string]target) []guideEntry {
	out := make([]guideEntry, 0, len(guide))
	for _, ref := range guide {
		target := targetForHref(ref.Href, targets)
		typ := strings.TrimSpace(ref.Type)
		if typ == "" {
			typ = "text"
		}
		title := strings.TrimSpace(ref.Title)
		if title == "" {
			title = typ
		}
		out = append(out, guideEntry{
			title:  title,
			typ:    typ,
			posFID: [2]int{target.chunkSeq, target.chunkOffset},
		})
	}
	return out
}

func targetForHref(href string, targets map[string]target) target {
	if target, ok := targets[href]; ok {
		return target
	}
	base, _, _ := strings.Cut(href, "#")
	if target, ok := targets[base]; ok {
		return target
	}
	return target{}
}

func encint(value int) []byte {
	if value < 0 {
		value = 0
	}
	var out []byte
	for {
		out = append(out, byte(value&0x7f))
		value >>= 7
		if value == 0 {
			break
		}
	}
	out[0] |= 0x80
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

type cncx struct {
	order   []string
	offsets map[string]int
}

func newCNCX() *cncx {
	return &cncx{offsets: map[string]int{}}
}

func (c *cncx) add(s string) {
	if _, ok := c.offsets[s]; ok {
		return
	}
	c.offsets[s] = -1
	c.order = append(c.order, s)
}

func (c *cncx) offset(s string) int {
	if c.offsets[s] >= 0 {
		return c.offsets[s]
	}
	_ = c.records()
	return c.offsets[s]
}

func (c *cncx) records() [][]byte {
	var records [][]byte
	var buf bytes.Buffer
	offset := 0
	for _, s := range c.order {
		rawText := truncateUTF8([]byte(s), 500)
		raw := append(encint(len(rawText)), rawText...)
		if buf.Len()+len(raw) > 0x10000-1024 && buf.Len() > 0 {
			records = append(records, align4(buf.Bytes()))
			buf.Reset()
			offset = len(records) * 0x10000
		}
		c.offsets[s] = offset
		buf.Write(raw)
		offset += len(raw)
	}
	if buf.Len() > 0 {
		records = append(records, align4(buf.Bytes()))
	}
	return records
}

func align4(data []byte) []byte {
	pad := (4 - len(data)%4) % 4
	if pad == 0 {
		return data
	}
	out := make([]byte, len(data)+pad)
	copy(out, data)
	return out
}

func maskShift(mask byte) uint {
	switch mask {
	case 1, 3:
		return 0
	case 2:
		return 1
	case 4, 12:
		return 2
	case 8:
		return 3
	case 16:
		return 4
	case 32:
		return 5
	case 64:
		return 6
	case 128:
		return 7
	default:
		return 0
	}
}

func decimal10(n int) string {
	return fmt.Sprintf("%010d", n)
}

func truncateUTF8(data []byte, limit int) []byte {
	if len(data) <= limit {
		return data
	}
	end := limit
	for end > 0 && !utf8.Valid(data[:end]) {
		end--
	}
	if end <= 0 {
		return nil
	}
	return data[:end]
}

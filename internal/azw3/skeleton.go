package azw3

import "unicode/utf8"

func chunkBytes(data []byte, size int) []textRecord {
	if len(data) == 0 {
		return []textRecord{{data: []byte{}, start: 0}}
	}
	chunks := make([]textRecord, 0, (len(data)+size-1)/size)
	start := 0
	for start < len(data) {
		end := min(start+size, len(data))
		chunk := make([]byte, end-start)
		copy(chunk, data[start:end])
		overlapEnd := end + utf8OverlapLength(data, end)
		overlap := make([]byte, overlapEnd-end)
		copy(overlap, data[end:overlapEnd])
		chunks = append(chunks, textRecord{data: chunk, overlap: overlap, start: start})
		start = end
	}
	return chunks
}

func utf8OverlapLength(data []byte, end int) int {
	if end <= 0 || end >= len(data) {
		return 0
	}
	lead := end - 1
	for lead > 0 && end-lead < utf8.UTFMax && data[lead]&0xc0 == 0x80 {
		lead--
	}
	width := 0
	switch b := data[lead]; {
	case b < 0x80:
		width = 1
	case b >= 0xc2 && b <= 0xdf:
		width = 2
	case b >= 0xe0 && b <= 0xef:
		width = 3
	case b >= 0xf0 && b <= 0xf4:
		width = 4
	default:
		return 0
	}
	missing := width - (end - lead)
	if missing <= 0 || end+missing > len(data) {
		return 0
	}
	return missing
}

func safeChunkEnd(data []byte, start, limit int) int {
	if limit >= len(data) {
		return len(data)
	}
	end := limit
	for end > start && !utf8.Valid(data[start:end]) {
		end--
	}
	if tagEnd := tagBoundary(data, start, end); tagEnd > start {
		end = tagEnd
	}
	if blockEnd := blockBoundary(data, start, end); blockEnd > start {
		end = blockEnd
	}
	if end <= start {
		end = limit
		for end > start && !utf8.Valid(data[start:end]) {
			end--
		}
	}
	if end <= start {
		_, sz := utf8.DecodeRune(data[start:])
		if sz <= 0 {
			return min(start+1, len(data))
		}
		return min(start+sz, len(data))
	}
	return end
}

func tagBoundary(data []byte, start, end int) int {
	lastOpen := lastIndexByte(data[start:end], '<')
	lastClose := lastIndexByte(data[start:end], '>')
	if lastOpen > lastClose {
		return start + lastOpen
	}
	return end
}

func blockBoundary(data []byte, start, end int) int {
	window := data[start:end]
	best := -1
	for _, marker := range [][]byte{
		[]byte("</p>"),
		[]byte("</h1>"),
		[]byte("</h2>"),
		[]byte("</h3>"),
		[]byte("</section>"),
		[]byte("</body>"),
	} {
		if idx := lastIndex(window, marker); idx > best {
			best = idx + len(marker)
		}
	}
	if best > 0 && end-(start+best) < 768 {
		return start + best
	}
	return end
}

func splitSkeletonChunks(rendered []byte) ([]byte, [][]byte, int) {
	openEnd := bodyInsertOffset(rendered)
	closeStart := lastIndex(rendered, []byte("</body>"))
	if openEnd <= 0 || closeStart < openEnd {
		return rendered, nil, 0
	}
	body := rendered[openEnd:closeStart]
	if sectionOpenEnd, sectionCloseStart, ok := singleSectionRange(body); ok {
		insertOffset := openEnd + sectionOpenEnd
		skeleton := make([]byte, 0, len(rendered)-(sectionCloseStart-sectionOpenEnd))
		skeleton = append(skeleton, rendered[:insertOffset]...)
		skeleton = append(skeleton, rendered[openEnd+sectionCloseStart:]...)
		chunks := splitContentChunks(body[sectionOpenEnd:sectionCloseStart], 8192)
		return skeleton, chunks, insertOffset
	}

	skeleton := make([]byte, 0, len(rendered)-(closeStart-openEnd))
	skeleton = append(skeleton, rendered[:openEnd]...)
	skeleton = append(skeleton, rendered[closeStart:]...)

	chunks := splitContentChunks(body, 8192)
	return skeleton, chunks, openEnd
}

func splitContentChunks(data []byte, size int) [][]byte {
	if len(data) == 0 {
		return nil
	}
	var chunks [][]byte
	start := 0
	for start < len(data) {
		end := safeChunkEnd(data, start, min(start+size, len(data)))
		raw := make([]byte, end-start)
		copy(raw, data[start:end])
		chunks = append(chunks, raw)
		start = end
	}
	return chunks
}

func buildSkelTable(docs []compiledDocument) []skelEntry {
	out := make([]skelEntry, 0, len(docs))
	for i, doc := range docs {
		out = append(out, skelEntry{
			name:       "SKEL" + decimal10(i),
			chunkCount: len(doc.chunks),
			startPos:   doc.flowStart,
			length:     len(doc.skeleton),
		})
	}
	return out
}

func bodyInsertOffset(data []byte) int {
	bodyStart := indexTag(data, "body")
	if bodyStart < 0 {
		return 0
	}
	close := indexByteFrom(data, '>', bodyStart)
	if close < 0 {
		return 0
	}
	return close + 1
}

func singleSectionRange(body []byte) (int, int, bool) {
	start := indexTag(body, "section")
	if start != 0 {
		return 0, 0, false
	}
	openEnd := indexByteFrom(body, '>', start)
	if openEnd < 0 {
		return 0, 0, false
	}
	openEnd++
	closeStart := lastIndex(body, []byte("</section>"))
	if closeStart < openEnd {
		return 0, 0, false
	}
	if closeStart+len("</section>") != len(body) {
		return 0, 0, false
	}
	return openEnd, closeStart, true
}

func indexTag(data []byte, tag string) int {
	for i := 0; i < len(data); i++ {
		if data[i] != '<' {
			continue
		}
		j := i + 1
		if j < len(data) && data[j] == '/' {
			continue
		}
		if j+len(tag) > len(data) {
			continue
		}
		if string(data[j:j+len(tag)]) != tag {
			continue
		}
		after := j + len(tag)
		if after == len(data) || data[after] == '>' || data[after] == ' ' || data[after] == '\n' || data[after] == '\t' {
			return i
		}
	}
	return -1
}

func lastIndex(data, sep []byte) int {
	for i := len(data) - len(sep); i >= 0; i-- {
		match := true
		for j := range sep {
			if data[i+j] != sep[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func lastIndexByte(data []byte, b byte) int {
	for i := len(data) - 1; i >= 0; i-- {
		if data[i] == b {
			return i
		}
	}
	return -1
}

func indexByteFrom(data []byte, b byte, start int) int {
	for i := start; i < len(data); i++ {
		if data[i] == b {
			return i
		}
	}
	return -1
}

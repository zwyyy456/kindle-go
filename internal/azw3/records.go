package azw3

func buildRecords(c compiledBook) ([]record, error) {
	recordLengths := make([]int, len(c.records))
	for i, chunk := range c.records {
		recordLengths[i] = len(chunk.data)
	}
	indexingTBS, err := buildIndexingTBS(c.tocTable, recordLengths)
	if err != nil {
		return nil, err
	}
	records := make([]record, 0, len(c.records)+12)
	records = append(records, record{})
	for _, chunk := range c.records {
		data := compressPalmDOC(chunk.data)
		data = append(data, chunk.overlap...)
		data = append(data, byte(len(chunk.overlap)))
		data = append(data, encodeTrailingData(indexingTBS[len(records)-1])...)
		records = append(records, record{data: data})
	}
	c.firstNonTextRecord = len(records)

	c.chunkIndexRecord = uint32(len(records))
	for _, data := range buildChunkIndex(c.chunkTable) {
		records = append(records, record{data: data})
	}
	c.skelIndexRecord = uint32(len(records))
	for _, data := range buildSkelIndex(c.skelTable) {
		records = append(records, record{data: data})
	}
	c.guideIndexRecord = nullIndex
	if guideRecords := buildGuideIndex(c.guideTable); len(guideRecords) > 0 {
		c.guideIndexRecord = uint32(len(records))
		for _, data := range guideRecords {
			records = append(records, record{data: data})
		}
	}
	c.ncxIndexRecord = nullIndex
	if ncxRecords := buildNCXIndex(c.tocTable); len(ncxRecords) > 0 {
		c.ncxIndexRecord = uint32(len(records))
		for _, data := range ncxRecords {
			records = append(records, record{data: data})
		}
	}
	c.firstResourceRecord = nullIndex
	if len(c.resources) > 0 {
		c.firstResourceRecord = uint32(len(records))
		for _, resource := range c.resources {
			records = append(records, record{data: resource.data})
		}
	}

	c.fdstRecord = uint32(len(records))
	c.fdstCount = uint32(len(c.flowBounds))
	records = append(records, record{data: buildFDST(c.flowBounds)})
	c.flisRecord = uint32(len(records))
	records = append(records, record{data: flisRecord})
	c.fcisRecord = uint32(len(records))
	records = append(records, record{data: buildFCIS(len(c.text))})
	records = append(records, record{data: []byte{0xe9, 0x8e, 0x0d, 0x0a}})

	exth := buildEXTH(c.metadata, c.coverResourceOffset, len(c.resources))
	records[0] = record{data: buildHeaderRecord(c, exth)}
	return records, nil
}

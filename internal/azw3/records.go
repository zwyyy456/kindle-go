package azw3

func buildRecords(c compiledBook) ([]record, error) {
	records := make([]record, 0, len(c.records)+12)
	records = append(records, record{})
	for _, chunk := range c.records {
		records = append(records, record{data: chunk.data})
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

	c.fdstRecord = uint32(len(records))
	c.fdstCount = 1
	records = append(records, record{data: buildFDST(len(c.text))})
	c.flisRecord = uint32(len(records))
	records = append(records, record{data: flisRecord})
	c.fcisRecord = uint32(len(records))
	records = append(records, record{data: buildFCIS(len(c.text))})
	records = append(records, record{data: []byte{0xe9, 0x8e, 0x0d, 0x0a}})

	exth := buildEXTH(c.metadata)
	records[0] = record{data: buildHeaderRecord(c, exth)}
	return records, nil
}

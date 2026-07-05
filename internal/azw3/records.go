package azw3

func buildRecords(c compiledBook) ([]record, error) {
	exth := buildEXTH(c.metadata)
	records := make([]record, 0, len(c.chunks)+2)
	records = append(records, record{data: buildHeaderRecord(c, exth)})
	for _, chunk := range c.chunks {
		records = append(records, record{data: chunk})
	}
	if len(c.toc) > 0 {
		records = append(records, record{data: c.toc})
	}
	return records, nil
}

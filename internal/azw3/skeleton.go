package azw3

func chunkBytes(data []byte, size int) [][]byte {
	if len(data) == 0 {
		return [][]byte{{}}
	}
	chunks := make([][]byte, 0, (len(data)+size-1)/size)
	for len(data) > 0 {
		n := size
		if len(data) < n {
			n = len(data)
		}
		chunk := make([]byte, n)
		copy(chunk, data[:n])
		chunks = append(chunks, chunk)
		data = data[n:]
	}
	return chunks
}

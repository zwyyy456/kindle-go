package azw3

import "github.com/flashdict/kindle2flashdict/internal/ebook"

const textRecordSize = 4096

type compiledBook struct {
	metadata ebook.Metadata
	text     []byte
	chunks   [][]byte
	toc      []byte
}

type record struct {
	data []byte
}

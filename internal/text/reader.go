package text

import (
	"bytes"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

type Decoded struct {
	Text    string
	Charset string
}

func ReadFile(path string) (Decoded, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Decoded{}, err
	}
	return Decode(data)
}

func Decode(data []byte) (Decoded, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(data) {
		return Decoded{Text: normalizeNewlines(string(data)), Charset: "utf-8"}, nil
	}

	reader := transform.NewReader(bytes.NewReader(data), simplifiedchinese.GB18030.NewDecoder())
	decoded, err := io.ReadAll(reader)
	if err != nil {
		return Decoded{}, err
	}
	return Decoded{Text: normalizeNewlines(string(decoded)), Charset: "gb18030"}, nil
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

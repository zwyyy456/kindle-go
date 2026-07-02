package text

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeUTF8(t *testing.T) {
	got, err := Decode([]byte("\xef\xbb\xbf第一章 开始"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Charset != "utf-8" {
		t.Fatalf("charset = %q", got.Charset)
	}
	if got.Text != "第一章 开始" {
		t.Fatalf("text = %q", got.Text)
	}
}

func TestDecodeGB18030(t *testing.T) {
	data, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte("第一章 开始"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Charset != "gb18030" {
		t.Fatalf("charset = %q", got.Charset)
	}
	if got.Text != "第一章 开始" {
		t.Fatalf("text = %q", got.Text)
	}
}

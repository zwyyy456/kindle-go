package transfer

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRFC6455WebSocketProbeEchoesTextAndBinary(t *testing.T) {
	conn, reader := startWebSocketProbe(t, http.Header{
		"Upgrade":               {"websocket"},
		"Connection":            {"Upgrade"},
		"Sec-Websocket-Key":     {"dGhlIHNhbXBsZSBub25jZQ=="},
		"Sec-Websocket-Version": {"13"},
	})

	headers := readHTTPHeaders(t, reader)
	if !strings.Contains(headers, "101 Switching Protocols") ||
		!strings.Contains(headers, "Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=") {
		t.Fatalf("handshake = %q", headers)
	}
	if strings.Contains(headers, "Sec-WebSocket-Protocol:") {
		t.Fatalf("server returned an unrequested subprotocol: %q", headers)
	}

	for _, test := range []struct {
		opcode  byte
		payload []byte
	}{
		{0x1, []byte("kindle-text")},
		{0x2, []byte{75, 73, 78, 68}},
	} {
		writeMaskedTestFrame(t, conn, test.opcode, test.payload)
		opcode, payload := readServerTestFrame(t, reader)
		if opcode != test.opcode || !bytes.Equal(payload, test.payload) {
			t.Fatalf("echo opcode/payload = %d/%q, want %d/%q", opcode, payload, test.opcode, test.payload)
		}
	}
}

func TestHixie76WebSocketProbeEchoesText(t *testing.T) {
	conn, reader := startWebSocketProbe(t, http.Header{
		"Upgrade":            {"WebSocket"},
		"Connection":         {"Upgrade"},
		"Origin":             {"http://example.com"},
		"Sec-Websocket-Key1": {"4 @1  46546xW%0l 1 5"},
		"Sec-Websocket-Key2": {"12998 5 Y3 1  .P00"},
	})

	key3 := []byte("^n:ds[4U")
	if _, err := conn.Write(key3); err != nil {
		t.Fatal(err)
	}
	headers := readHTTPHeaders(t, reader)
	if !strings.Contains(headers, "101 WebSocket Protocol Handshake") ||
		!strings.Contains(headers, "Sec-WebSocket-Location: ws://probe.test/ws-probe") {
		t.Fatalf("handshake = %q", headers)
	}
	challenge := make([]byte, 16)
	binary.BigEndian.PutUint32(challenge[0:4], mustHixieKeyPart(t, "4 @1  46546xW%0l 1 5"))
	binary.BigEndian.PutUint32(challenge[4:8], mustHixieKeyPart(t, "12998 5 Y3 1  .P00"))
	copy(challenge[8:], key3)
	wantDigest := md5.Sum(challenge)
	gotDigest := make([]byte, len(wantDigest))
	if _, err := io.ReadFull(reader, gotDigest); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDigest, wantDigest[:]) {
		t.Fatalf("challenge digest = %x, want %x", gotDigest, wantDigest)
	}

	if _, err := conn.Write(append(append([]byte{0x00}, []byte("kindle-hixie")...), 0xff)); err != nil {
		t.Fatal(err)
	}
	frame, err := reader.ReadBytes(0xff)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(frame); got != "\x00kindle-hixie\xff" {
		t.Fatalf("echo frame = %q", got)
	}
}

func startWebSocketProbe(t *testing.T, headers http.Header) (net.Conn, *bufio.Reader) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	rw := bufio.NewReadWriter(bufio.NewReader(serverConn), bufio.NewWriter(serverConn))
	writer := &hijackResponseWriter{
		header: make(http.Header),
		conn:   serverConn,
		rw:     rw,
	}
	request := httptest.NewRequest(http.MethodGet, "http://probe.test/ws-probe", nil)
	request.Header = headers
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&controlServer{}).handleWebSocketProbe(writer, request)
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		<-done
	})
	return clientConn, bufio.NewReader(clientConn)
}

type hijackResponseWriter struct {
	header http.Header
	conn   net.Conn
	rw     *bufio.ReadWriter
}

func (w *hijackResponseWriter) Header() http.Header {
	return w.header
}

func (w *hijackResponseWriter) Write(payload []byte) (int, error) {
	return w.rw.Write(payload)
}

func (w *hijackResponseWriter) WriteHeader(statusCode int) {
}

func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if w.conn == nil || w.rw == nil {
		return nil, nil, fmt.Errorf("hijack unavailable")
	}
	return w.conn, w.rw, nil
}

func readHTTPHeaders(t *testing.T, reader *bufio.Reader) string {
	t.Helper()
	var headers strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		headers.WriteString(line)
		if line == "\r\n" {
			return headers.String()
		}
	}
}

func writeMaskedTestFrame(t *testing.T, writer io.Writer, opcode byte, payload []byte) {
	t.Helper()
	mask := [4]byte{1, 2, 3, 4}
	frame := []byte{0x80 | opcode, 0x80 | byte(len(payload))}
	frame = append(frame, mask[:]...)
	for i, value := range payload {
		frame = append(frame, value^mask[i%len(mask)])
	}
	if _, err := writer.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func readServerTestFrame(t *testing.T, reader *bufio.Reader) (byte, []byte) {
	t.Helper()
	first, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	second, err := reader.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	if second&0x80 != 0 || second&0x7f >= 126 {
		t.Fatalf("unexpected test frame header: %x %x", first, second)
	}
	payload := make([]byte, int(second&0x7f))
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatal(err)
	}
	return first & 0x0f, payload
}

func mustHixieKeyPart(t *testing.T, key string) uint32 {
	t.Helper()
	value, err := hixieKeyPart(key)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

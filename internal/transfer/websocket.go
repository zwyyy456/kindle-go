package transfer

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	webSocketGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxWebSocketPayload = 64 << 10
)

func (s *controlServer) handleWebSocketProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !headerHasToken(r.Header, "Connection", "upgrade") ||
		!strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		http.Error(w, "WebSocket upgrade required", http.StatusUpgradeRequired)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "WebSocket hijacking unavailable", http.StatusInternalServerError)
		return
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	var variant string
	switch {
	case r.Header.Get("Sec-WebSocket-Version") != "":
		variant = "RFC 6455"
		err = serveRFC6455Probe(conn, rw, r)
	case r.Header.Get("Sec-WebSocket-Key1") != "" && r.Header.Get("Sec-WebSocket-Key2") != "":
		variant = "Hixie-76"
		err = serveHixie76Probe(conn, rw, r)
	default:
		variant = "Hixie-75"
		err = serveHixie75Probe(conn, rw, r)
	}
	if s.Stdout != nil {
		fmt.Fprintf(s.Stdout, "WebSocket probe handshake: %s\n", variant)
		if err != nil {
			fmt.Fprintf(s.Stdout, "WebSocket probe ended: %s\n", printableLine(err.Error()))
		}
	}
}

func serveRFC6455Probe(conn net.Conn, rw *bufio.ReadWriter, r *http.Request) error {
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		return errors.New("RFC 6455 request has no Sec-WebSocket-Key")
	}
	sum := sha1.Sum([]byte(key + webSocketGUID))
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Accept: %s\r\n",
		base64.StdEncoding.EncodeToString(sum[:])); err != nil {
		return err
	}
	if protocol := firstHeaderToken(r.Header.Get("Sec-WebSocket-Protocol")); protocol != "" {
		if _, err := fmt.Fprintf(rw, "Sec-WebSocket-Protocol: %s\r\n", protocol); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprint(rw, "\r\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	for {
		opcode, payload, err := readRFC6455Frame(rw.Reader)
		if err != nil {
			return err
		}
		switch opcode {
		case 0x1, 0x2:
			if err := writeRFC6455Frame(rw.Writer, opcode, payload); err != nil {
				return err
			}
			if err := rw.Flush(); err != nil {
				return err
			}
		case 0x8:
			_ = writeRFC6455Frame(rw.Writer, 0x8, nil)
			_ = rw.Flush()
			return nil
		case 0x9:
			if err := writeRFC6455Frame(rw.Writer, 0xa, payload); err != nil {
				return err
			}
			if err := rw.Flush(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported RFC 6455 opcode %d", opcode)
		}
	}
}

func readRFC6455Frame(r *bufio.Reader) (byte, []byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	if header[0]&0x80 == 0 {
		return 0, nil, errors.New("fragmented WebSocket frame is unsupported")
	}
	opcode := header[0] & 0x0f
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7f)
	switch length {
	case 126:
		var value uint16
		if err := binary.Read(r, binary.BigEndian, &value); err != nil {
			return 0, nil, err
		}
		length = uint64(value)
	case 127:
		if err := binary.Read(r, binary.BigEndian, &length); err != nil {
			return 0, nil, err
		}
	}
	if !masked {
		return 0, nil, errors.New("client WebSocket frame is not masked")
	}
	if length > maxWebSocketPayload {
		return 0, nil, errors.New("WebSocket probe payload is too large")
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%len(mask)]
	}
	return opcode, payload, nil
}

func writeRFC6455Frame(w *bufio.Writer, opcode byte, payload []byte) error {
	if err := w.WriteByte(0x80 | opcode); err != nil {
		return err
	}
	switch {
	case len(payload) < 126:
		if err := w.WriteByte(byte(len(payload))); err != nil {
			return err
		}
	case len(payload) <= 0xffff:
		if err := w.WriteByte(126); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, uint16(len(payload))); err != nil {
			return err
		}
	default:
		if err := w.WriteByte(127); err != nil {
			return err
		}
		if err := binary.Write(w, binary.BigEndian, uint64(len(payload))); err != nil {
			return err
		}
	}
	_, err := w.Write(payload)
	return err
}

func serveHixie76Probe(conn net.Conn, rw *bufio.ReadWriter, r *http.Request) error {
	part1, err := hixieKeyPart(r.Header.Get("Sec-WebSocket-Key1"))
	if err != nil {
		return err
	}
	part2, err := hixieKeyPart(r.Header.Get("Sec-WebSocket-Key2"))
	if err != nil {
		return err
	}
	var key3 [8]byte
	if _, err := io.ReadFull(rw.Reader, key3[:]); err != nil {
		return fmt.Errorf("read Hixie-76 key3: %w", err)
	}
	challenge := make([]byte, 16)
	binary.BigEndian.PutUint32(challenge[0:4], part1)
	binary.BigEndian.PutUint32(challenge[4:8], part2)
	copy(challenge[8:], key3[:])
	digest := md5.Sum(challenge)
	if err := writeHixieHandshake(rw.Writer, r, true); err != nil {
		return err
	}
	if _, err := rw.Write(digest[:]); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	return echoHixieTextFrames(rw)
}

func serveHixie75Probe(conn net.Conn, rw *bufio.ReadWriter, r *http.Request) error {
	if err := writeHixieHandshake(rw.Writer, r, false); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	return echoHixieTextFrames(rw)
}

func hixieKeyPart(key string) (uint32, error) {
	var digits strings.Builder
	spaces := 0
	for _, r := range key {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r == ' ':
			spaces++
		}
	}
	if digits.Len() == 0 || spaces == 0 {
		return 0, errors.New("invalid Hixie-76 key")
	}
	value, err := strconv.ParseUint(digits.String(), 10, 64)
	if err != nil || value%uint64(spaces) != 0 || value/uint64(spaces) > uint64(^uint32(0)) {
		return 0, errors.New("invalid Hixie-76 key quotient")
	}
	return uint32(value / uint64(spaces)), nil
}

func writeHixieHandshake(w *bufio.Writer, r *http.Request, secure bool) error {
	prefix := "WebSocket"
	if secure {
		prefix = "Sec-WebSocket"
	}
	scheme := "ws"
	if r.TLS != nil {
		scheme = "wss"
	}
	if _, err := fmt.Fprint(w, "HTTP/1.1 101 WebSocket Protocol Handshake\r\n"+
		"Upgrade: WebSocket\r\n"+
		"Connection: Upgrade\r\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s-Origin: %s\r\n", prefix, r.Header.Get("Origin")); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s-Location: %s://%s%s\r\n", prefix, scheme, r.Host, r.URL.RequestURI()); err != nil {
		return err
	}
	protocol := r.Header.Get("Sec-WebSocket-Protocol")
	if protocol == "" {
		protocol = r.Header.Get("WebSocket-Protocol")
	}
	if protocol != "" {
		if _, err := fmt.Fprintf(w, "%s-Protocol: %s\r\n", prefix, firstHeaderToken(protocol)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprint(w, "\r\n")
	return err
}

func echoHixieTextFrames(rw *bufio.ReadWriter) error {
	for {
		start, err := rw.ReadByte()
		if err != nil {
			return err
		}
		if start == 0xff {
			end, err := rw.ReadByte()
			if err != nil {
				return err
			}
			if end == 0x00 {
				return nil
			}
			return errors.New("invalid Hixie close frame")
		}
		if start != 0x00 {
			return fmt.Errorf("unsupported Hixie frame start 0x%x", start)
		}
		payload, err := rw.ReadBytes(0xff)
		if err != nil {
			return err
		}
		payload = payload[:len(payload)-1]
		if len(payload) > maxWebSocketPayload {
			return errors.New("Hixie probe payload is too large")
		}
		if err := rw.WriteByte(0x00); err != nil {
			return err
		}
		if _, err := rw.Write(payload); err != nil {
			return err
		}
		if err := rw.WriteByte(0xff); err != nil {
			return err
		}
		if err := rw.Flush(); err != nil {
			return err
		}
	}
}

func firstHeaderToken(value string) string {
	return strings.TrimSpace(strings.Split(value, ",")[0])
}

func headerHasToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

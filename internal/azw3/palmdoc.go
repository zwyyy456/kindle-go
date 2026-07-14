package azw3

import "fmt"

// compressPalmDOC encodes one independently decompressible PalmDOC text record.
// Matches use the most recent three-byte prefix within the 2 KiB PalmDOC window.
func compressPalmDOC(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	out := make([]byte, 0, len(src))
	last := make(map[uint32]int, len(src))
	keyAt := func(pos int) (uint32, bool) {
		if pos+2 >= len(src) {
			return 0, false
		}
		return uint32(src[pos])<<16 | uint32(src[pos+1])<<8 | uint32(src[pos+2]), true
	}
	remember := func(pos int) {
		if key, ok := keyAt(pos); ok {
			last[key] = pos
		}
	}
	matchAt := func(pos int) (distance, length int) {
		key, ok := keyAt(pos)
		if !ok {
			return 0, 0
		}
		previous, ok := last[key]
		if !ok || pos-previous > 2047 {
			return 0, 0
		}
		length = 3
		for length < 10 && pos+length < len(src) && src[previous+length] == src[pos+length] {
			length++
		}
		return pos - previous, length
	}

	for pos := 0; pos < len(src); {
		if pos+1 < len(src) && src[pos] == ' ' && src[pos+1] >= 0x40 && src[pos+1] <= 0x7f {
			out = append(out, src[pos+1]^0x80)
			remember(pos)
			remember(pos + 1)
			pos += 2
			continue
		}
		if distance, length := matchAt(pos); length >= 3 {
			code := uint16(0x8000 | distance<<3 | (length - 3))
			out = append(out, byte(code>>8), byte(code))
			for i := 0; i < length; i++ {
				remember(pos + i)
			}
			pos += length
			continue
		}
		if src[pos] >= 0x09 && src[pos] <= 0x7f {
			out = append(out, src[pos])
			remember(pos)
			pos++
			continue
		}

		start := pos
		for pos < len(src) && pos-start < 8 {
			if pos > start {
				if src[pos] >= 0x09 && src[pos] <= 0x7f {
					break
				}
				if _, length := matchAt(pos); length >= 3 {
					break
				}
			}
			remember(pos)
			pos++
		}
		out = append(out, byte(pos-start))
		out = append(out, src[start:pos]...)
	}
	return out
}

func decompressPalmDOC(src []byte) ([]byte, error) {
	out := make([]byte, 0, len(src)*2)
	for pos := 0; pos < len(src); {
		b := src[pos]
		pos++
		switch {
		case b == 0 || (b >= 0x09 && b <= 0x7f):
			out = append(out, b)
		case b >= 1 && b <= 8:
			end := pos + int(b)
			if end > len(src) {
				return nil, fmt.Errorf("PalmDOC literal run exceeds record")
			}
			out = append(out, src[pos:end]...)
			pos = end
		case b >= 0x80 && b <= 0xbf:
			if pos >= len(src) {
				return nil, fmt.Errorf("PalmDOC back-reference is truncated")
			}
			code := uint16(b)<<8 | uint16(src[pos])
			pos++
			distance := int((code & 0x3ff8) >> 3)
			length := int(code&7) + 3
			if distance == 0 || distance > len(out) {
				return nil, fmt.Errorf("PalmDOC back-reference distance %d exceeds output %d", distance, len(out))
			}
			for i := 0; i < length; i++ {
				out = append(out, out[len(out)-distance])
			}
		default:
			out = append(out, ' ', b^0x80)
		}
	}
	return out, nil
}

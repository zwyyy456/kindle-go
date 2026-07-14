package azw3

import (
	"bytes"
	"fmt"
	"sort"
)

// compressPalmDOC encodes one independently decompressible PalmDOC text record.
// Its match selection mirrors Calibre's conservative PalmDOC encoder: matches
// are longest-first, most-recent-first, and never overlap the current position.
func compressPalmDOC(src []byte) []byte {
	if len(src) == 0 {
		return nil
	}
	out := make([]byte, 0, len(src))
	positions := make(map[uint32][]int, len(src))
	keyAt := func(pos int) (uint32, bool) {
		if pos+2 >= len(src) {
			return 0, false
		}
		return uint32(src[pos])<<16 | uint32(src[pos+1])<<8 | uint32(src[pos+2]), true
	}
	for pos := range src {
		if key, ok := keyAt(pos); ok {
			positions[key] = append(positions[key], pos)
		}
	}
	matchAt := func(pos int) (distance, length int) {
		key, ok := keyAt(pos)
		if !ok {
			return 0, 0
		}
		candidates := positions[key]
		for length = 10; length >= 3; length-- {
			if pos+length > len(src) {
				continue
			}
			// Starting at or before pos-length prevents the match source from
			// overlapping bytes that have not yet been encoded.
			last := sort.SearchInts(candidates, pos-length+1) - 1
			for i := last; i >= 0; i-- {
				previous := candidates[i]
				distance = pos - previous
				if distance > 2047 {
					break
				}
				if bytes.Equal(src[previous:previous+length], src[pos:pos+length]) {
					return distance, length
				}
			}
		}
		return 0, 0
	}

	for pos := 0; pos < len(src); {
		if pos > 10 && len(src)-pos > 10 {
			if distance, length := matchAt(pos); length >= 3 {
				code := uint16(0x8000 | distance<<3 | (length - 3))
				out = append(out, byte(code>>8), byte(code))
				pos += length
				continue
			}
		}

		current := src[pos]
		pos++
		if current == ' ' && pos < len(src) && src[pos] >= 0x40 && src[pos] <= 0x7f {
			out = append(out, src[pos]^0x80)
			pos++
			continue
		}
		if current == 0 || (current > 8 && current < 0x80) {
			out = append(out, current)
			continue
		}

		literal := []byte{current}
		for pos < len(src) && len(literal) < 8 {
			current = src[pos]
			if current == 0 || (current > 8 && current < 0x80) {
				break
			}
			literal = append(literal, current)
			pos++
		}
		out = append(out, byte(len(literal)))
		out = append(out, literal...)
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

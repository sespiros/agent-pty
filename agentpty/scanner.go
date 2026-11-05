package agentpty

import "bytes"

var marker = []byte("\x1b]7501;")

// maxSequence is the protocol's limit for one sequence, OSC through ST.
const maxSequence = 4096

// found is one OSC 7501 sequence cut out of the output.
type found struct {
	offset int    // where it was in the cleaned output
	body   []byte // the bytes between "7501;" and the terminator
	size   int    // the whole sequence, OSC through ST
}

// scanner splits a byte stream into terminal output and OSC 7501 bodies.
// pst.Scanner only observes; the relay must also remove the sequences, so it
// keeps its own. A sequence or a marker cut at a read boundary is held back
// until the next feed. A run longer than the sequence limit without a
// terminator is not a report and passes through.
type scanner struct {
	pending []byte
}

func (s *scanner) feed(data []byte) ([]byte, []found) {
	if len(s.pending) > 0 {
		data = append(s.pending, data...)
		s.pending = nil
	}
	var out []byte
	var seqs []found
	for len(data) > 0 {
		start := bytes.Index(data, marker)
		if start < 0 {
			keep := heldPrefix(data)
			out = append(out, data[:len(data)-keep]...)
			s.pending = append([]byte(nil), data[len(data)-keep:]...)
			break
		}
		out = append(out, data[:start]...)
		bodyStart := start + len(marker)
		end, size := terminator(data, bodyStart)
		if end < 0 {
			if len(data)-start > maxSequence {
				out = append(out, data[start:bodyStart]...)
				data = data[bodyStart:]
				continue
			}
			s.pending = append([]byte(nil), data[start:]...)
			break
		}
		body := append([]byte(nil), data[bodyStart:end]...)
		seqs = append(seqs, found{offset: len(out), body: body, size: end + size - start})
		data = data[end+size:]
	}
	return out, seqs
}

// heldPrefix is the length of the longest tail of data that could start a
// marker.
func heldPrefix(data []byte) int {
	for n := min(len(marker)-1, len(data)); n > 0; n-- {
		if bytes.HasPrefix(marker, data[len(data)-n:]) {
			return n
		}
	}
	return 0
}

// terminator returns the position and length of the first BEL or ST at or
// after start, or -1.
func terminator(data []byte, start int) (int, int) {
	bel := bytes.IndexByte(data[start:], 0x07)
	st := bytes.Index(data[start:], []byte("\x1b\\"))
	switch {
	case bel < 0 && st < 0:
		return -1, 0
	case st < 0 || (bel >= 0 && bel < st):
		return start + bel, 1
	default:
		return start + st, 2
	}
}

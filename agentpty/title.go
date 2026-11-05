package agentpty

import "bytes"

// maxTitle bounds how much of a window title sequence is kept while it is
// incomplete; a longer one is not a title anyone shows.
const maxTitle = 1024

// titles watches the output for the window title a program sets with OSC 0
// or OSC 2 (ESC ] 0 ; text BEL, or ESC ] 2 ; text ST). It only observes: the
// sequence still reaches the terminal. A sequence cut at a read boundary is
// completed by the next feed.
type titles struct {
	partial []byte // an unfinished sequence from the last feed, from ESC
}

// feed returns the last complete title in data, and whether there was one.
func (t *titles) feed(data []byte) (string, bool) {
	if len(t.partial) > 0 {
		data = append(t.partial, data...)
		t.partial = nil
	}
	var last string
	found := false
	for {
		i := bytes.Index(data, []byte("\x1b]"))
		if i < 0 {
			if n := len(data); n > 0 && data[n-1] == 0x1b {
				t.partial = []byte{0x1b}
			}
			return last, found
		}
		data = data[i:]
		if len(data) < 4 {
			t.partial = append([]byte(nil), data...)
			return last, found
		}
		if (data[2] != '0' && data[2] != '2') || data[3] != ';' {
			data = data[2:]
			continue
		}
		end, size := terminator(data, 4)
		if end < 0 {
			if len(data) <= maxTitle {
				t.partial = append([]byte(nil), data...)
			}
			return last, found
		}
		last, found = string(data[4:end]), true
		data = data[end+size:]
	}
}

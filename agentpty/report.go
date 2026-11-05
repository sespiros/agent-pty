package agentpty

import (
	"bytes"
	"errors"

	"github.com/adamvduke/pst"
)

// reply is the feature detection reply Rex gives, so the agents see the same
// terminal they see there.
var reply = []byte("\x1b]7501;?:states=idle,working,done,blocked,error:kinds=permission,question,auth\x1b\\")

type parsed int

const (
	discarded parsed = iota
	probe
	accepted
)

// parse turns one sequence body into a report. size is the whole sequence.
func parse(body []byte, size int) (pst.Report, parsed) {
	if size > maxSequence {
		return pst.Report{}, discarded
	}
	r, err := pst.ParseBody(body)
	switch {
	case errors.Is(err, pst.ErrQuery):
		return pst.Report{}, probe
	case err != nil:
		return pst.Report{}, discarded
	}
	// WORKAROUND: pst v0.1.0 skips an id pair with a byte outside the value
	// set and applies the report to the root record. The spec ignores a
	// report whose id does not match the grammar, so that a malformed id
	// cannot overwrite the root. Drop when pst discards it itself.
	if r.ID == "" && hasKey(body, "id") {
		return pst.Report{}, discarded
	}
	return r, accepted
}

// hasKey reports whether body has a pair with this key, well formed or not.
func hasKey(body []byte, key string) bool {
	for _, pair := range bytes.Split(body, []byte(":")) {
		k, _, ok := bytes.Cut(pair, []byte("="))
		if ok && string(bytes.Trim(k, " \t")) == key {
			return true
		}
	}
	return false
}

package events

import (
	"crypto/rand"
	"encoding/hex"
)

func newEventID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "evt-unknown"
	}
	return "evt-" + hex.EncodeToString(buf)
}

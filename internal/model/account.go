package model

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

// NewAccountID returns an identity that survives renames and reordering. The
// console sends it back with an empty key to mean "keep the stored key", which
// positional matching cannot express once rows can be reordered or removed.
func NewAccountID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand does not fail on supported platforms; keep identities
		// unique anyway instead of returning an empty id.
		return "acc_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "acc_" + hex.EncodeToString(raw[:])
}

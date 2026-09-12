package envelope

import (
	"crypto/sha256"
	"encoding/hex"
)

// Digest is the sha256 of some bytes, hex encoded, as an envelope spells it.
//
// It is here rather than inline in four bricks because an artifact's digest travels in the
// item that attached it, and the store is content addressed by exactly this: two steps
// producing identical bytes store one copy, and a digest computed a second way would be a
// second object for the same content.
func Digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

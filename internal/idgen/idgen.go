// Package idgen provides a minimal, stdlib-only UUID v4 generator, used in place of
// the google/uuid package the spec assumes (no Go module proxy access in this sandbox).
package idgen

import (
	"crypto/rand"
	"fmt"
)

// New returns a random RFC 4122 version-4 UUID string.
func New() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

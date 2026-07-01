package store

import (
	"crypto/rand"
	"math/big"
)

// randIntn returns a cryptographically random int in [0, n).
func randIntn(n int) int {
	if n <= 0 {
		return 0
	}
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(nBig.Int64())
}

// shuffleCatalog returns a new shuffled copy (Fisher-Yates) using crypto/rand.
func shuffleCatalog(in []CatalogObject) []CatalogObject {
	out := make([]CatalogObject, len(in))
	copy(out, in)
	for i := len(out) - 1; i > 0; i-- {
		j := randIntn(i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

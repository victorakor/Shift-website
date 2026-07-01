// Package auth implements registration, recovery, and session handling (Section 5).
//
// Deviation from spec: the spec calls for bcrypt (golang.org/x/crypto/bcrypt), which
// lives outside the Go standard library. This sandbox has no access to the Go module
// proxy, so HashSecret/VerifySecret below use a hand-rolled PBKDF2-HMAC-SHA256 with a
// random salt and a high iteration count instead — cryptographically sound, stdlib-only.
// See progress.md Section 0 for the swap path back to bcrypt.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

const (
	pbkdf2Iterations = 100_000
	saltLen          = 16
	keyLen           = 32
)

// pbkdf2 derives a key using HMAC-SHA256, per RFC 8018.
func pbkdf2(password, salt []byte, iterations, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var derived []byte

	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		be := make([]byte, 4)
		binary.BigEndian.PutUint32(be, uint32(block))
		prf.Write(be)
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)

		for i := 1; i < iterations; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		derived = append(derived, t...)
	}
	return derived[:keyLen]
}

// HashSecret returns a string encoding "pbkdf2$iterations$saltB64$hashB64".
func HashSecret(secret string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	derived := pbkdf2([]byte(secret), salt, pbkdf2Iterations, keyLen)
	return fmt.Sprintf("pbkdf2$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived),
	), nil
}

// VerifySecret checks a plaintext secret against a hash produced by HashSecret.
func VerifySecret(secret, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false, errors.New("invalid hash format")
	}
	var iterations int
	if _, err := fmt.Sscanf(parts[1], "%d", &iterations); err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false, err
	}
	got := pbkdf2([]byte(secret), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

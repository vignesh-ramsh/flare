package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
)

// TokenPrefix starts every access token. It makes a leaked token recognisable to
// secret scanners, and to people reading a log.
const TokenPrefix = "flr_"

// A token is the prefix and 32 random bytes in URL-safe base64: 47 characters.
var tokenRE = regexp.MustCompile(`^flr_[A-Za-z0-9_-]{43}$`)

// NewToken returns a new random access token. Only its hash is ever stored, so
// the token can be shown just once, when it is created.
func NewToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b[:])
}

// LooksLikeToken reports whether s has the form of a token. It lets a malformed
// credential be refused without touching the database.
func LooksLikeToken(s string) bool { return tokenRE.MatchString(s) }

// HashToken returns what is stored for token: its SHA-256, in hex. A token has
// 256 bits of randomness, so unlike a password it doesn't need a slow hash.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// newID returns a random identifier for a user or token record.
func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

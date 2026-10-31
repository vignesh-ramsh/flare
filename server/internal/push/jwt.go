package push

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
)

// The push services sign in with short-lived JWTs: VAPID (Web Push) and APNs use
// ES256, Google's service accounts RS256. These are the two signatures, with the
// little else a JWT needs, so that no JWT library is required.

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// signingInput returns "header.claims" in base64url.
func signingInput(header, claims any) (string, error) {
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	c, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return b64(h) + "." + b64(c), nil
}

// signES256 returns a JWT signed with an ECDSA P-256 key. The signature is the
// two 32-byte numbers r and s joined, not the ASN.1 form that Go and OpenSSL
// produce by default.
func signES256(key *ecdsa.PrivateKey, header, claims any) (string, error) {
	input, err := signingInput(header, claims)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + b64(sig), nil
}

// verifyES256 checks a signature made by signES256. The server never needs it;
// tests do, to check that what is sent is what a push service would accept.
func verifyES256(pub *ecdsa.PublicKey, jwt string) bool {
	i := lastDot(jwt)
	if i < 0 {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(jwt[i+1:])
	if err != nil || len(sig) != 64 {
		return false
	}
	sum := sha256.Sum256([]byte(jwt[:i]))
	return ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
}

// signRS256 returns a JWT signed with an RSA key.
func signRS256(key *rsa.PrivateKey, header, claims any) (string, error) {
	input, err := signingInput(header, claims)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return input + "." + b64(sig), nil
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

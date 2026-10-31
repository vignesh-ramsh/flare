package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Params are the cost settings of Argon2id.
type Params struct {
	Time      uint32 // passes over the memory
	MemoryKiB uint32
	Threads   uint8
}

// DefaultParams follow the OWASP recommendation for Argon2id (19 MiB, two
// passes, one lane). Hashing takes a few tens of milliseconds.
var DefaultParams = Params{Time: 2, MemoryKiB: 19 * 1024, Threads: 1}

const (
	saltLength = 16
	keyLength  = 32
	// The limits a stored hash's parameters must stay within, so that a damaged or
	// hostile value can't make verification use unbounded time or memory.
	maxStoredMemoryKiB = 512 * 1024
	maxStoredTime      = 10
	maxStoredThreads   = 16
)

// Password length limits, in bytes. The upper limit keeps hashing cheap.
const (
	MinPasswordLength = 10
	MaxPasswordLength = 256
)

// ValidPassword checks a new password against the rules.
func ValidPassword(pw string) error {
	switch {
	case len(pw) < MinPasswordLength:
		return fmt.Errorf("the password must be at least %d characters long", MinPasswordLength)
	case len(pw) > MaxPasswordLength:
		return fmt.Errorf("the password must be at most %d bytes long", MaxPasswordLength)
	case !utf8.ValidString(pw):
		return errors.New("the password must be valid UTF-8 text")
	}
	return nil
}

// Hasher creates and checks password hashes. It lets only a few run at once:
// each takes real memory, and without a limit a flood of login attempts could use
// all of it.
type Hasher struct {
	params Params
	slots  chan struct{}

	dummyOnce sync.Once
	dummy     string
}

// NewHasher returns a Hasher that allows maxConcurrent hashes at a time.
func NewHasher(p Params, maxConcurrent int) *Hasher {
	return &Hasher{params: p, slots: make(chan struct{}, max(maxConcurrent, 1))}
}

func (h *Hasher) acquire(ctx context.Context) (release func(), err error) {
	select {
	case h.slots <- struct{}{}:
		return func() { <-h.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var b64 = base64.RawStdEncoding

// Hash returns the Argon2id hash of password in the standard encoded form,
// `$argon2id$v=19$m=...,t=...,p=...$salt$key`, which carries everything needed to
// check it later.
func (h *Hasher) Hash(ctx context.Context, password string) (string, error) {
	release, err := h.acquire(ctx)
	if err != nil {
		return "", err
	}
	defer release()

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, h.params.Time, h.params.MemoryKiB, h.params.Threads, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, h.params.MemoryKiB, h.params.Time, h.params.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether password matches encoded. The error is for a hash that
// can't be read, or a cancelled context; a wrong password is (false, nil).
func (h *Hasher) Verify(ctx context.Context, password, encoded string) (bool, error) {
	p, salt, want, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	release, err := h.acquire(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	got := argon2.IDKey([]byte(password), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Dummy returns a valid hash of a password nobody knows. Checking a login
// against it when the user doesn't exist makes that case take as long as a wrong
// password for a real user, so response times don't reveal which users exist.
func (h *Hasher) Dummy(ctx context.Context) string {
	h.dummyOnce.Do(func() {
		random := make([]byte, 24)
		_, _ = rand.Read(random)
		h.dummy, _ = h.Hash(context.Background(), base64.RawURLEncoding.EncodeToString(random))
	})
	return h.dummy
}

func parseHash(encoded string) (p Params, salt, key []byte, err error) {
	bad := errors.New("unreadable password hash")
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return p, nil, nil, bad
	}
	var m, t, par uint64
	for _, kv := range strings.Split(parts[3], ",") {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return p, nil, nil, bad
		}
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return p, nil, nil, bad
		}
		switch name {
		case "m":
			m = n
		case "t":
			t = n
		case "p":
			par = n
		default:
			return p, nil, nil, bad
		}
	}
	if m < 8 || m > maxStoredMemoryKiB || t < 1 || t > maxStoredTime || par < 1 || par > maxStoredThreads {
		return p, nil, nil, bad
	}
	if salt, err = b64.DecodeString(parts[4]); err != nil || len(salt) < 8 || len(salt) > 64 {
		return p, nil, nil, bad
	}
	if key, err = b64.DecodeString(parts[5]); err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, bad
	}
	return Params{Time: uint32(t), MemoryKiB: uint32(m), Threads: uint8(par)}, salt, key, nil
}

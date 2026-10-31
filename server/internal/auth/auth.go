// Package auth decides who a request comes from and what that person may do.
//
// There are two kinds of rules. A server-wide default says what anyone may do
// who has no more specific rule. Access rules give particular users, or everyone,
// particular rights on topics matching a pattern. Users prove who they are with a
// password or an access token.
package auth

import (
	"errors"
	"fmt"
	"time"
)

// Permission is something a request wants to do to a topic.
type Permission int

const (
	// Read is subscribing to a topic.
	Read Permission = iota
	// Write is publishing to a topic.
	Write
)

func (p Permission) String() string {
	if p == Read {
		return "read"
	}
	return "write"
}

// Access is what someone may do with a topic.
type Access string

const (
	ReadWrite Access = "read-write"
	ReadOnly  Access = "read-only"
	WriteOnly Access = "write-only"
	DenyAll   Access = "deny-all"
)

// ParseAccess checks that s names an access level.
func ParseAccess(s string) (Access, error) {
	switch a := Access(s); a {
	case ReadWrite, ReadOnly, WriteOnly, DenyAll:
		return a, nil
	}
	return "", fmt.Errorf("must be one of read-write, read-only, write-only or deny-all (found %q)", s)
}

// Allows reports whether this level lets someone do p. An unknown level allows
// nothing, so a mistake can never open anything up.
func (a Access) Allows(p Permission) bool {
	switch p {
	case Read:
		return a == ReadWrite || a == ReadOnly
	case Write:
		return a == ReadWrite || a == WriteOnly
	}
	return false
}

// Open reports whether this level lets anyone do anything at all.
func (a Access) Open() bool { return a.Allows(Read) || a.Allows(Write) }

// union returns the level that allows everything either a or b allows.
func union(a, b Access) Access {
	read := a.Allows(Read) || b.Allows(Read)
	write := a.Allows(Write) || b.Allows(Write)
	switch {
	case read && write:
		return ReadWrite
	case read:
		return ReadOnly
	case write:
		return WriteOnly
	}
	return DenyAll
}

// Errors that say why a request was not allowed. They may be wrapped, so test
// for them with errors.Is.
var (
	// ErrUnauthorized: nobody is logged in, and what was asked needs someone who
	// is. The client should authenticate and try again.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden: the person is logged in, but may not do this.
	ErrForbidden = errors.New("forbidden")

	// ErrBadCredentials: a password or token was given and it is wrong, unknown,
	// expired or revoked. It is never treated as "anonymous": a request that
	// tries to log in and fails is refused.
	ErrBadCredentials = errors.New("invalid credentials")

	// ErrThrottled: too many failed attempts from one address. See ThrottledError.
	ErrThrottled = errors.New("too many failed attempts")
)

// ThrottledError is ErrThrottled with how long the client must wait.
type ThrottledError struct{ RetryAfter time.Duration }

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("%v: try again in %s", ErrThrottled, e.RetryAfter.Round(time.Second))
}
func (e *ThrottledError) Is(target error) bool { return target == ErrThrottled }

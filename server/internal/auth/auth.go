// Package auth decides who may read from and write to which topic.
//
// Today that is a single server-wide policy. User accounts, access tokens and
// per-topic rules will plug in behind the same Authorizer interface, so the
// HTTP handlers won't change.
package auth

import (
	"errors"
	"fmt"
	"net/http"
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

// ErrForbidden is returned, possibly wrapped, when access is refused.
var ErrForbidden = errors.New("forbidden")

// Authorizer decides whether a request may do p to topic.
type Authorizer interface {
	Authorize(r *http.Request, topic string, p Permission) error
}

// DefaultAccess is what anyone who has not been given specific rights may do.
type DefaultAccess string

const (
	ReadWrite DefaultAccess = "read-write"
	ReadOnly  DefaultAccess = "read-only"
	WriteOnly DefaultAccess = "write-only"
	DenyAll   DefaultAccess = "deny-all"
)

// ParseDefaultAccess checks that s names a default access level.
func ParseDefaultAccess(s string) (DefaultAccess, error) {
	switch a := DefaultAccess(s); a {
	case ReadWrite, ReadOnly, WriteOnly, DenyAll:
		return a, nil
	}
	return "", fmt.Errorf("must be one of read-write, read-only, write-only or deny-all (found %q)", s)
}

// Open reports whether anyone at all can publish or subscribe.
func (a DefaultAccess) Open() bool { return a != DenyAll }

// Policy applies one DefaultAccess to every request.
type Policy struct{ access DefaultAccess }

var _ Authorizer = Policy{}

// NewPolicy returns an Authorizer that gives everyone the given access.
func NewPolicy(a DefaultAccess) Policy { return Policy{access: a} }

func (p Policy) Authorize(_ *http.Request, _ string, perm Permission) error {
	allowed := false
	switch perm {
	case Read:
		allowed = p.access == ReadWrite || p.access == ReadOnly
	case Write:
		allowed = p.access == ReadWrite || p.access == WriteOnly
	}
	if !allowed {
		return fmt.Errorf("%w: this server does not allow %s access", ErrForbidden, perm)
	}
	return nil
}

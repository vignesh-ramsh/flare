package auth

import (
	"errors"
	"testing"
	"time"
)

func TestAccessLevels(t *testing.T) {
	tests := []struct {
		access      Access
		read, write bool
	}{
		{ReadWrite, true, true},
		{ReadOnly, true, false},
		{WriteOnly, false, true},
		{DenyAll, false, false},
		{"", false, false}, // an unset or unknown level must never open anything up
		{"read_write", false, false},
		{"READ-WRITE", false, false},
	}
	for _, tt := range tests {
		if tt.access.Allows(Read) != tt.read || tt.access.Allows(Write) != tt.write {
			t.Errorf("%q: read=%v write=%v, want %v and %v", tt.access, tt.access.Allows(Read), tt.access.Allows(Write), tt.read, tt.write)
		}
		if tt.access.Open() != (tt.read || tt.write) {
			t.Errorf("%q: Open() = %v", tt.access, tt.access.Open())
		}
	}
	if Access(ReadWrite).Allows(Permission(7)) {
		t.Error("an unknown permission was allowed")
	}
}

func TestParseAccess(t *testing.T) {
	for _, ok := range []string{"read-write", "read-only", "write-only", "deny-all"} {
		if a, err := ParseAccess(ok); err != nil || string(a) != ok {
			t.Errorf("ParseAccess(%q) = %q, %v", ok, a, err)
		}
	}
	for _, bad := range []string{"", "open", "Read-Write", "deny"} {
		if _, err := ParseAccess(bad); err == nil {
			t.Errorf("ParseAccess(%q) succeeded", bad)
		}
	}
}

func TestUnion(t *testing.T) {
	for _, tt := range []struct{ a, b, want Access }{
		{ReadOnly, WriteOnly, ReadWrite}, {WriteOnly, ReadOnly, ReadWrite}, {ReadOnly, ReadOnly, ReadOnly},
		{DenyAll, ReadOnly, ReadOnly}, {DenyAll, DenyAll, DenyAll}, {ReadWrite, DenyAll, ReadWrite},
	} {
		if got := union(tt.a, tt.b); got != tt.want {
			t.Errorf("union(%s, %s) = %s, want %s", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestThrottledErrorIsErrThrottled(t *testing.T) {
	var err error = &ThrottledError{RetryAfter: 90 * time.Second}
	if !errors.Is(err, ErrThrottled) || errors.Is(err, ErrForbidden) {
		t.Error("a ThrottledError should match ErrThrottled and nothing else")
	}
	if got := err.Error(); got != "too many failed attempts: try again in 1m30s" {
		t.Errorf("message = %q", got)
	}
}

func TestPermissionString(t *testing.T) {
	if Read.String() != "read" || Write.String() != "write" {
		t.Error("permission names")
	}
}

package auth

import (
	"errors"
	"net/http/httptest"
	"testing"
)

func TestPolicy(t *testing.T) {
	tests := []struct {
		access      DefaultAccess
		read, write bool
	}{
		{ReadWrite, true, true},
		{ReadOnly, true, false},
		{WriteOnly, false, true},
		{DenyAll, false, false},
		{"", false, false}, // an unset or unknown level must never open the server
		{"read_write", false, false},
	}
	r := httptest.NewRequest("GET", "/t", nil)
	for _, tt := range tests {
		p := NewPolicy(tt.access)
		for perm, want := range map[Permission]bool{Read: tt.read, Write: tt.write} {
			err := p.Authorize(r, "t", perm)
			if (err == nil) != want {
				t.Errorf("%q %v: err = %v, want allowed=%v", tt.access, perm, err, want)
			}
			if err != nil && !errors.Is(err, ErrForbidden) {
				t.Errorf("%q %v: error %v should wrap ErrForbidden", tt.access, perm, err)
			}
		}
	}
}

func TestParseDefaultAccess(t *testing.T) {
	for _, ok := range []string{"read-write", "read-only", "write-only", "deny-all"} {
		if a, err := ParseDefaultAccess(ok); err != nil || string(a) != ok {
			t.Errorf("ParseDefaultAccess(%q) = %q, %v", ok, a, err)
		}
	}
	for _, bad := range []string{"", "open", "Read-Write", "deny"} {
		if _, err := ParseDefaultAccess(bad); err == nil {
			t.Errorf("ParseDefaultAccess(%q) succeeded", bad)
		}
	}
	if !ReadWrite.Open() || !ReadOnly.Open() || !WriteOnly.Open() || DenyAll.Open() {
		t.Error("Open() is wrong for some level")
	}
}

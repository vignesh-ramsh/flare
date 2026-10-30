package id

import (
	"sort"
	"testing"
	"time"
)

func TestGeneratedIDsAreValidUUIDv7(t *testing.T) {
	var g Generator
	for range 1000 {
		if s := g.New(time.Now()); !Valid(s) {
			t.Fatalf("%q is not a valid UUIDv7", s)
		}
	}
}

func TestIDsStrictlyIncrease(t *testing.T) {
	cases := map[string]func(i int) time.Time{
		"same instant, far more IDs than the counter holds": func(int) time.Time { return time.UnixMilli(1_700_000_000_000) },
		"clock stepping backwards":                          func(i int) time.Time { return time.UnixMilli(1_700_000_000_000 - int64(i)) },
		"clock moving forwards":                             func(i int) time.Time { return time.UnixMilli(1_700_000_000_000 + int64(i/3)) },
	}
	for name, clock := range cases {
		t.Run(name, func(t *testing.T) {
			var g Generator
			prev := ""
			for i := range 20_000 { // 5x the 12-bit counter, so it overflows
				s := g.New(clock(i))
				if s <= prev {
					t.Fatalf("id %d (%s) does not sort after the previous one (%s)", i, s, prev)
				}
				prev = s
			}
		})
	}
}

func TestIDsSortLikeTheirCreationOrder(t *testing.T) {
	var g Generator
	ids := make([]string, 500)
	for i := range ids {
		ids[i] = g.New(time.UnixMilli(1_700_000_000_000 + int64(i)*7))
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatal("IDs created in order do not sort in that order")
	}
}

func TestTimeRoundTrips(t *testing.T) {
	var g Generator
	want := time.UnixMilli(1_761_800_123_456)
	if got := Time(g.New(want)); !got.Equal(want) {
		t.Fatalf("Time = %v, want %v", got, want)
	}
}

func TestBoundarySeparatesBeforeFromAfter(t *testing.T) {
	var g Generator
	at := time.UnixMilli(1_761_800_000_000)
	b := Boundary(at)
	if !Valid(b) {
		t.Fatalf("boundary %q is not a valid ID", b)
	}
	before := g.New(at.Add(-time.Millisecond))
	atSame := g.New(at) // created in the same millisecond as the boundary
	after := g.New(at.Add(time.Millisecond))
	if !(before < b) {
		t.Errorf("an ID from before the boundary (%s) should sort before it (%s)", before, b)
	}
	if !(b < atSame && b < after) {
		t.Errorf("IDs created at or after the boundary should sort after it: %s %s %s", b, atSame, after)
	}
}

func TestValidRejectsOtherStrings(t *testing.T) {
	for _, s := range []string{
		"", "hello", "01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c2", // too short
		"01933C3E-7A4F-7CC1-9B2E-5F1D6A8E4C21",  // upper case
		"01933c3e-7a4f-4cc1-9b2e-5f1d6a8e4c21",  // version 4
		"01933c3e-7a4f-7cc1-cb2e-5f1d6a8e4c21",  // bad variant
		"01933c3e7a4f7cc19b2e5f1d6a8e4c21",      // no dashes
		"01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21x", // trailing text
	} {
		if Valid(s) {
			t.Errorf("Valid(%q) = true, want false", s)
		}
	}
	if !Valid("01933c3e-7a4f-7cc1-9b2e-5f1d6a8e4c21") {
		t.Error("a well-formed UUIDv7 was rejected")
	}
}

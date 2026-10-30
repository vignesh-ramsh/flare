// Package id generates and parses message IDs.
//
// An ID is a UUIDv7 (RFC 9562): a 48-bit millisecond timestamp, then a 12-bit
// counter and random bits. Written as the usual lower-case hex string, IDs sort
// by creation time with a plain string comparison, which is what lets a client
// say "everything after the last ID I saw", even if that message has since
// expired.
package id

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strconv"
	"time"
)

var pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Valid reports whether s is a lower-case UUIDv7 string.
func Valid(s string) bool { return pattern.MatchString(s) }

// Generator hands out strictly increasing IDs. Within one millisecond, and even
// if the clock steps backwards, a 12-bit counter keeps each ID larger than the
// last. It is not safe for concurrent use: the caller serialises access, which
// the hub needs to do anyway to keep commit order equal to ID order.
type Generator struct {
	lastMS int64
	seq    uint16
}

// New returns an ID for the time now, larger than every ID returned before.
func (g *Generator) New(now time.Time) string {
	if ms := now.UnixMilli(); ms > g.lastMS {
		g.lastMS = ms
		g.seq = randomSeq()
	} else {
		g.seq++
		if g.seq > 0xFFF { // counter exhausted: borrow the next millisecond
			g.lastMS++
			g.seq = randomSeq()
		}
	}

	var b [16]byte
	ms := uint64(g.lastMS)
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	b[6] = 0x70 | byte(g.seq>>8)
	b[7] = byte(g.seq)
	_, _ = rand.Read(b[8:]) // never fails since Go 1.24
	b[8] = 0x80 | b[8]&0x3F
	return format(b)
}

// randomSeq starts the counter in the lower half of its range, leaving room to
// count up within a millisecond without borrowing.
func randomSeq() uint16 {
	var r [2]byte
	_, _ = rand.Read(r[:])
	return (uint16(r[0])<<8 | uint16(r[1])) & 0x7FF
}

func format(b [16]byte) string {
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}

// Boundary returns the smallest ID that could have been created at time t. It
// sorts before every ID created at or after t and after every ID created
// earlier, so "IDs greater than Boundary(t)" means "messages since t".
func Boundary(t time.Time) string {
	var b [16]byte
	ms := uint64(t.UnixMilli())
	for i := 0; i < 6; i++ {
		b[i] = byte(ms >> (8 * (5 - i)))
	}
	b[6] = 0x70
	b[8] = 0x80
	return format(b)
}

// Time returns the creation time recorded in a valid ID, to the millisecond.
func Time(s string) time.Time {
	ms, err := strconv.ParseInt(s[0:8]+s[9:13], 16, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

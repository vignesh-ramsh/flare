package realtime

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vignesh-ramsh/flare/server/internal/id"
)

// Cursor says where a subscriber wants to start.
type Cursor struct {
	// Live means only messages published from now on, with no catching up.
	Live bool
	// After is the ID of the last message the subscriber already has: it is sent
	// everything that sorts after it. "" means from the start of the cache.
	After string
}

var (
	// LiveCursor starts at "now".
	LiveCursor = Cursor{Live: true}
	// AllCursor starts at the oldest cached message.
	AllCursor = Cursor{}
)

// ParseCursor reads the value of a subscriber's `since` setting:
//
//   - "all": every cached message
//   - a message ID: everything after that message, even if it has expired
//   - a duration with a unit, such as "10m" or "2h": messages from that long ago
//   - a unix timestamp in seconds (10 digits, as dates have been since 2001):
//     messages since then
//
// A bare small number such as "30" is refused rather than guessed at: it could
// mean 30 seconds or 30 seconds after 1970.
func ParseCursor(s string, now time.Time) (Cursor, error) {
	switch {
	case s == "all":
		return AllCursor, nil
	case id.Valid(s):
		return Cursor{After: s}, nil
	case s != "" && strings.Trim(s, "0123456789") == "":
		secs, err := strconv.ParseInt(s, 10, 64)
		if err != nil || secs < minTimestamp {
			return Cursor{}, fmt.Errorf("invalid since value %q: a number is read as a unix timestamp, so durations need a unit (for example %ss)", s, s)
		}
		return since(time.Unix(secs, 0)), nil
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return since(now.Add(-d)), nil
	}
	return Cursor{}, fmt.Errorf("invalid since value %q: use all, a message ID, a duration such as 10m, or a unix timestamp", s)
}

// minTimestamp is the earliest number ParseCursor accepts as a unix timestamp:
// September 2001, when timestamps reached ten digits.
const minTimestamp = 1_000_000_000

// since is the cursor for "messages from t on".
func since(t time.Time) Cursor {
	if t.UnixMilli() <= 0 {
		return AllCursor
	}
	return Cursor{After: id.Boundary(t)}
}
